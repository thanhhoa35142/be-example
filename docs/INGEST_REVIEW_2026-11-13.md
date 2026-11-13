# Review kiến trúc ingest — 2026-11-13

Phạm vi: `docs/INGEST_STRATEGY.md` đối chiếu với `internal/modules/ingest/**`,
`db/queries/ingest.sql`, `internal/infrastructure/persistence/ingest_repo.go`,
`cmd/worker/main.go`, và **dữ liệu thật** trong DB local (`picnic-postgres`,
các pack NL/BE/LU/AT/CH đã ingest).

## Kết luận nhanh

Hướng kiến trúc **đúng** và nên giữ: raw layer + canonical layer, osmium
ngoài tx, 1 tx/pack, spot ID bất biến, provenance qua `t_spot_sources`,
freshness gate, LISTEN/NOTIFY để đánh thức scoring.

Nhưng có **1 bug dữ liệu nghiêm trọng (P0)** đang làm hỏng spot thật, vài bug
logic về lifecycle, và planner **không** "pure + in-memory" như doc mô tả —
đó là nguyên nhân chính của thời gian chạy dài (Vienna 9 phút).

---

## P0 — osmium export mỗi closed way 2 lần → xoá area, hạ quality, tạo trùng

`osmium export --geometry-types=point,linestring,polygon -u type_id` xuất
một closed way (vd. `leisure=park`) **hai lần**:

- `w<id>` — LineString
- `a<2·id>` — MultiPolygon (relation → `a<2·id+1>`)

Bằng chứng (raw layer):

| prefix | geometry | candidate | amenity |
|---|---|---|---|
| `a` | MULTIPOLYGON | 3,923 | 83,152 |
| `w` | LINESTRING | 3,837 | 82,960 |

Hậu quả, theo thứ tự xử lý (`ListCandidateFeatures ORDER BY source_id` → `a…` trước `w…`):

1. `a…` tạo/cập nhật spot với polygon.
2. `w…` (cùng tên, centroid < 30 m) → **fallback match** vào chính spot đó
   (`service.go:417-439`) → `UpdateSpot` với `AreaGeoJSON=nil` →
   `UpdateIngestSpot` set **`area = NULL`** (`ingest.sql:136-137`) và
   quality của LineString (avg 30 vs 34 của polygon).
   - **1,147 spot** có cả link `a`+`w`, raw `a` là polygon nhưng `t_spots.area IS NULL`.
3. Quality tụt < `KeepPending` (35) → `NextStatus` trả `hidden/below_quality_floor`
   (`policy.go`) — và `hidden` là terminal → **kẹt vĩnh viễn**.
   - 295 spot hidden `below_quality_floor` mang cặp `w`+`a`; tổng 493 spot hidden có primary `w…`.
4. Way không tên → không fallback → **spot trùng**: 279 cặp, **81 cặp cả hai đang live**.
5. Amenity bị đếm đôi: **82,945** amenity feature có cả bản `w` và `a`.
6. Area bị xoá → amenity không gắn được (xem P0-3).
7. Doc §4 nói ID "`w<id>`, `r<id>` identical across Overpass and osmium" — **sai**:
   osmium cho `a…`. Đổi adapter = mồ côi toàn bộ spot dạng area.

**Fix đề xuất**

- `osmium.go` (`Features()`): chuẩn hoá ID — `a<even>` → `w<n/2>`, `a<odd>` →
  `r<(n-1)/2>`. Khi gặp cả line và area cho cùng ID, **giữ polygon**:
  - đơn giản nhất: bỏ qua feature LineString nếu là closed ring (first == last coord), hoặc
  - `InsertIngestFeature ... ON CONFLICT (pack_id, source, source_id) DO UPDATE ... WHERE ST_Dimension(EXCLUDED.geom) > ST_Dimension(t_ingest_features.geom)`, hoặc
  - dùng config của `osmium export` (`linear_tags`/`area_tags`) để không xuất closed way thành linear — cần kiểm `man osmium-export` của version trong image `pickhour-osmium` trước khi chọn.
- Migration dữ liệu một lần (`000005_osm_id_normalize`):
  1. Re-key `t_spot_sources`: `a<2n>` → `w<n>`, `a<2n+1>` → `r<n>`; nếu `w<n>` đã trỏ vào cùng spot → xoá bản `a`.
  2. 81 cặp spot trùng đang live → `merged` vào spot cũ hơn (giữ spot ID, không xoá).
  3. Spot `hidden` với `status_reason='below_quality_floor'` có cặp `w`+`a` → re-evaluate (đưa về `pending` để run sau tính lại).
- **Rollout**: chạy `-dry-run` trước. Guard hiện chỉ đếm `MissingActive`, không đếm
  close `sources_moved` (P1-4) — re-key sai có thể đóng hàng loạt mà guard không chặn.

## P0-2 — Khoá `curated` (D4) bị vi phạm

`service.go:487-508` và `:610-623`: nhánh `if sp.Curated` chỉ bỏ qua đổi
status, nhưng `UpdateSpot` vẫn chạy **vô điều kiện** → name/attrs/geom/area/quality
của spot curated (và `merged`, `hidden`, `closed_by_report`) bị ghi đè mỗi run.
Hiện `count(*) where curated = 0` nên chưa hỏng dữ liệu — fix trước khi admin bắt đầu curate.

Fix: `if sp.Curated || sp.Status in {hidden, merged} || closed_by_report { st.SkippedCurated++; UpsertSource(...); continue }`
(vẫn upsert source để `missing_runs` đúng, nhưng không đụng `t_spots`).

## P0-3 — Amenity: chậm 160×, gắn sai, và stats sai

`NearestSpotForPoint` (`ingest.sql:185-193`), gọi 1 lần/amenity (`service.go:747`):

- Lọc `s.area IS NOT NULL` → **mọi spot dạng điểm** (orphan-cluster site, picnic_site node,
  và 1,147 spot bị xoá area ở P0) **không bao giờ** có amenity.
- Rank theo `ST_Distance(s.geom, …)` (anchor), không theo area.
- `ST_DWithin(s.area::geography, …)` — cast sang geography trên polygon lớn → GiST
  index trên `area` (geometry) chỉ dùng được một phần, phần exact rất đắt.
- `st.AmenitiesLinked++` chạy cả khi không match → Vienna báo `amenities_linked=31,790`,
  thực tế chỉ **3,193** amenity được gắn (~10%). Số "24,682 amenity links" trong doc §10 là số amenity feature, không phải link.

**Benchmark** (pack `vienna`, 31,790 amenity, chạy trong DB — chưa tính round-trip Go↔PG, nên N+1 thực tế còn tệ hơn):

| Biến thể | Thời gian | Ghi chú |
|---|---|---|
| A. Hiện tại (N+1, loop server-side) | **210.3 s** | ≈ 40% của 9 phút Vienna |
| C. Set-based, **giữ nguyên semantics** | 211.0 s | → chi phí nằm ở `area::geography`, không phải round-trip |
| B. Set-based + `&& ST_Expand` prefilter + exact geography + bỏ dup `w` | 15.2 s | 214 (spot, type), exact |
| D. Set-based, geometry-only (`ST_DWithin(area, g, 0.0045°)` + KNN `<->`) | **1.28 s** | ~160×, khoảng cách xấp xỉ theo độ |

Fix: một câu `INSERT INTO t_spot_amenities ... SELECT ... CROSS JOIN LATERAL (... ORDER BY s.area <-> a.g LIMIT 1) GROUP BY spot, type`
— prefilter geometry (`&&` + `ST_DWithin` theo độ, nới theo `1/cos(lat)`), exact
geography chỉ trên vài ứng viên còn lại; dùng `COALESCE(area, geom::geometry)` để spot
dạng điểm cũng nhận amenity; ghi `count` đúng bằng `count(*)` thay vì `+1` từng dòng.
Stats lấy từ `RowsAffected`.

---

## P1 — Lifecycle / vận hành

1. **Một pack lỗi chặn tất cả pack phía sau, mãi mãi** — `main.go` `runIngest`
   `return` ngay ở pack lỗi đầu tiên. Guard trip là **deterministic** với cùng extract →
   pack đứng sau theo thứ tự `slug` không bao giờ chạy. "Failed runs self-heal" (doc §2) không đúng với guard.
   Fix: chạy tiếp, gom lỗi (`errors.Join`), chỉ giữ `.pbf` của extract có pack lỗi; thêm flag `-force-guard` cho admin.

2. **Spot do importer ẩn không bao giờ quay lại** — `NextStatus` coi `hidden` là terminal,
   nhưng chính importer ẩn spot với `below_quality_floor`, `vanished_before_review`, `sources_moved`.
   Fix: phân biệt theo `status_reason` — importer-hidden thì được re-evaluate; admin-hidden thì không.

3. **Spot `closed/source_missing` không reopen khi source quay lại nguyên vẹn** —
   `policy.go` chỉ reopen khi `SourceChanged` (hash đổi). Feature tạm bị `access=private`
   rồi revert, hoặc extract hỏng 2 run dưới ngưỡng guard → hash y hệt → closed vĩnh viễn.
   Fix: `closed` + `status_reason='source_missing'` + source có mặt lại → `active/source_returned`.

4. **Guard chỉ bảo vệ một phần**:
   - Chỉ đếm `MissingActive`; close `sources_moved` và hide pending không bị tính.
   - Run hỏng **đầu tiên** luôn commit (missing_runs 0→1 chưa đủ 2): raw layer bị thay
     bằng dữ liệu cụt, amenities bị `ClearAmenities` + gắn lại từ dữ liệu cụt.
   Fix: guard sớm trên tỉ lệ "source vắng mặt run này" (vd. > 15% primary source của pack
   không thấy → abort) trước khi đụng amenities; đếm tổng mọi transition phá huỷ.

5. **Disk model mâu thuẫn với code** — `RemoveExtract` xoá `.pbf`, nhưng `pbfFor`
   (`osmium.go:194-206`) `os.Stat` `.pbf` và lỗi nếu thiếu → mọi re-run phải
   `EnsureExtracts` tải lại cả file → mtime mới > artifact → rebuild. "Same-week re-ingest reuses artifacts" (§10) **sai**.
   - `EnsureExtracts` không stamp `extract_fetched_at` → khi `ExtractFetchedAt` nil (refresh chưa chạy theo lịch — §10 thừa nhận), freshness gate luôn mở → **tải lại toàn bộ extract mỗi ngày**.
   - `EnsureExtracts` tải **tất cả** extract trước rồi mới ingest → đỉnh disk = tổng raw `.pbf` (~60 GB cho 30-50 nước) — đúng cái mà disk model muốn tránh trên VPS 40 GB.
   - Artifact của `filterVersion` cũ không bao giờ bị dọn; `.prev.osm.pbf` vô nghĩa khi xoá cả hai.
   Fix: xử lý **tuần tự theo extract**: download → build artifact → xoá `.pbf` → ingest các pack của extract đó → extract kế. `pbfFor` chấp nhận thiếu `.pbf` nếu artifact đã có (so mtime artifact với `extract_fetched_at` thay vì mtime file). Dùng `If-Modified-Since`/ETag với Geofabrik. Dọn artifact khác `filterVersion` hiện hành.

6. **FK `t_ingest_features.run_id → t_job_runs` không có `ON DELETE`** — `PurgeJobRuns`
   (`retention.sql`) sẽ lỗi FK khi xoá job run cũ mà raw layer của một pack chưa re-ingest
   vẫn tham chiếu; một dòng vi phạm làm cả batch fail. Fix: `ON DELETE SET NULL`.

7. **Orphan cluster toàn member foreign-owned** (đọc code, chưa đo): spot vẫn được
   `InsertSpot` (`service.go:635-655`), sau đó member bị skip → spot không có primary →
   `Sourceless` đóng/ẩn ngay trong cùng run → **mỗi run sinh thêm một spot rác**. Hay
   gặp ở ranh giới Randstad (bbox các pack chồng nhau). Fix: lọc member foreign trước khi quyết định create.

8. **N+1 trong planner** — `SpotBySource` 1 lần/candidate và 2 lần/cluster member,
   `SpotsNear` 1 lần/candidate chưa match, `UpsertSource`/`UpdateSpot` từng dòng, `InsertRaw` từng dòng.

## P2

- Anchor dùng `ST_Centroid` (`ListCandidateFeatures`), doc §3 nói `ST_PointOnSurface`: **65 spot** có anchor nằm ngoài chính area của nó (13 beach, 43 park, 9 site) — pin rơi xuống biển với bãi biển dạng dải.
- `ClosedByReport: strings.Contains(sp.StatusReason, "report")` — dễ vỡ; so sánh bằng hằng `closed_by_report`.
- Cluster tách đôi → cả hai nửa có thể re-find cùng spot cũ (vòng lặp dừng ở member đầu tìm thấy).
- Hand-off ownership: straddler do pack A sở hữu trôi ra khỏi bbox A → A đóng nó, B thấy `ForeignOwned` và skip → mất vĩnh viễn.
- `t_job_runs` có 3 dòng `finished_at < started_at` (~1 phút) — lệch đồng hồ giữa `now()` của DB và `time.Now()` của worker (WSL drift). Dùng một nguồn thời gian.
- `int2OrNull(0)` → quality 0 thành NULL; vô hại nhưng gây nhầm khi query.

---

## Doc ↔ code không khớp (doc là source of truth → cần sửa một trong hai)

| Doc | Thực tế |
|---|---|
| §2 "pure Go planner, in-memory inputs" | Planner gọi DB xen kẽ (N+1), không chạy được không cần DB |
| §3 anchor = `ST_PointOnSurface` | `ST_Centroid` |
| §4 ID `w/r` giống nhau giữa Overpass và osmium | osmium cho `a<2n>`/`a<2n+1>` |
| D4 importer không bao giờ mutate curated/hidden/merged | `UpdateSpot` chạy vô điều kiện |
| §2 "failed runs self-heal" | Guard trip lặp lại vĩnh viễn và chặn pack sau |
| §10 re-ingest cùng tuần dùng lại artifact | Phải tải lại `.pbf` |
| §10 "24,682 amenity links" | Là số amenity feature; link thật ≈ 10% |

---

## Hướng kiến trúc tốt hơn (đề xuất, theo thứ tự đáng làm)

1. **Plan/Apply thật sự tách rời.** Load một lần: `t_spot_sources` của pack (+ foreign
   trong bbox), `t_spots` của pack, candidate, absorbed, cluster, cặp fallback (tính bằng 1
   query spatial join set-based thay cho `SpotsNear` từng dòng) → `Plan(inputs) → []Op` thuần Go
   → golden test không cần DB → `Apply` bằng batch (`unnest`/`:copyfrom`). Dry-run in ra diff thay vì rollback.
2. **Raw layer bằng COPY.** sqlc hỗ trợ `:copyfrom` với pgx/v5 (giữ đúng quy tắc "chỉ sqlc"):
   COPY vào staging `geojson text` → `INSERT … SELECT ST_GeomFromGeoJSON` → set-based. Có thể
   stream vào staging **ngoài** tx chính (unlogged table, key theo `run_id`) để tx chính ngắn.
3. **Amenity set-based** (P0-3): 210 s → ~1-15 s.
4. **Pipeline theo extract** (P1-5) để disk model thật sự đạt 10-15 GB.
5. **Status machine theo `status_reason`** gom P1-2/P1-3 về một bảng transition rõ ràng, có test.

## Query tái lập bằng chứng

```sql
-- closed way xuất 2 lần
SELECT left(source_id,1), GeometryType(geom), role, count(*)
FROM t_ingest_features GROUP BY 1,2,3 ORDER BY 4 DESC;

-- area bị xoá bởi bản LineString
SELECT count(DISTINCT sp.id)
FROM t_spot_sources a
JOIN t_spot_sources w ON w.spot_id = a.spot_id
 AND w.source_id = 'w' || (substr(a.source_id,2)::bigint / 2)
JOIN t_spots sp ON sp.id = a.spot_id
JOIN t_ingest_features f ON f.source_id = a.source_id AND f.pack_id = sp.city_pack_id
WHERE a.source_id LIKE 'a%' AND sp.area IS NULL AND ST_Dimension(f.geom) = 2;   -- 1147

-- spot trùng w/a
SELECT count(*), count(*) FILTER (WHERE s1.status IN ('active','pending')
                                    AND s2.status IN ('active','pending'))
FROM t_spot_sources w
JOIN t_spot_sources a ON a.source_id = 'a' || (substr(w.source_id,2)::bigint * 2)
JOIN t_spots s1 ON s1.id = w.spot_id JOIN t_spots s2 ON s2.id = a.spot_id
WHERE w.source_id LIKE 'w%' AND w.spot_id <> a.spot_id;                          -- 279 | 81

-- anchor ngoài area
SELECT kind, count(*) FROM t_spots
WHERE area IS NOT NULL AND NOT ST_Intersects(area, geom::geometry) GROUP BY 1;
```

---

## Trạng thái sau khi sửa (2026-11-13)

Đã sửa: P0-1, P0-2, P0-3, P1-1…P1-8, anchor (P2), `closed_by_report` so sánh chính xác.
Phát hiện thêm trong lúc sửa và đã fix: **`geoBBox` cũ loại nhầm feature** — mọi mảng
2 phần tử bị coi là cặp toạ độ, nên LineString 2 điểm, Polygon có 1 lỗ, MultiPolygon 2
phần bị loại khỏi pack (test `TestGeoBBox`). 12 park xuất hiện trở lại; 76 bàn picnic
trước đây bị coi là orphan giờ được absorb đúng vào park, 16 orphan site cũ đóng `sources_moved`.

Thay đổi chính: migration `000005_ingest_fixes`, `db/queries/ingest.sql`, `osm/osmium.go`,
`osm/policy.go`, `ingest/service.go`, `ingest/extract.go`, `persistence/ingest_repo.go`,
`cmd/worker/main.go` (+ flag `-force-guard`). Test mới: 5 golden + policy + id + bbox.

### Benchmark (DB local, cùng artifact osmium, `-dry-run`)

| Đo | Trước | Sau |
|---|---|---|
| Vienna, toàn bộ pack run (cùng DB sau migration) | 310 s | **24,7 s** (12,6×) |
| Vienna steady state (không có gì thay đổi) | — | 13,4 s |
| Gắn amenity Vienna | 210 s (N+1, chạy trong DB) | 4,4 s |
| Stream geojsonseq NL (279 MB) | 21 s | 3,2 s (6,5×) |
| Steady state 8 pack (Lux…Zurich) | — | 4–14 s mỗi pack |

Lưu ý so sánh: bản cũ chạy trên DB **đã migrate** — ID đã đổi tên nên provenance của nó
trượt (tạo 91 spot, update 1.514), workload không hoàn toàn giống nhau. Hai dòng amenity
và stream là so sánh sạch (cùng input, cùng ngữ nghĩa đầu ra).

`stage_ms` mỗi pack nằm trong `t_job_runs.stats`. Phần nặng còn lại: `merge`
(ST_GeomFromGeoJSON set-based, 2–5 s) và `amenities` (1–4 s).

### Dữ liệu sau re-ingest

| Chỉ số | Trước | Sau |
|---|---|---|
| Spot live có polygon nguồn nhưng `area IS NULL` | 1.147 | 2 (orphan site gom từ bàn dạng polygon — đúng thiết kế) |
| Anchor nằm ngoài area (live) | 65 | 0 |
| Cặp spot trùng w/a | 279 (81 live) | 0 — 279 `merged/duplicate_osm_area` |
| Hidden `below_quality_floor` | 493 | 0 (re-score → pending/active) |
| Spot dạng điểm có amenity | 0 | 594 |
| Raw dòng LineString | 3.837 candidate + ~83k amenity | 235 (way không khép kín thật) |

- `cell_id` giờ theo anchor khi update (trước đây chỉ tính lúc insert): 34 spot live có cell
  cũ sau khi anchor chuyển sang `ST_PointOnSurface` → đã sửa ở lần chạy kế.
- Tham chiếu tới 279 twin đã merge: `t_alert_rules`, `t_crowd_reports`, `t_spot_reports`,
  `t_score_feedbacks` = 0 dòng (local). Migration vẫn chuyển `t_alert_rules` sang spot giữ
  lại; API đã redirect spot `merged` qua `MergedError`. Score/crowd estimate là dữ liệu dẫn xuất.

### Runbook deploy (bắt buộc)

Migration KHÔNG xoá amenity, nhưng việc khôi phục area, re-score spot hidden và dọn amenity
đếm đôi chỉ xảy ra ở **lần ingest đầu tiên sau migrate** — đừng chờ freshness gate:

1. Backup `t_spots`, `t_spot_sources`, `t_spot_amenities`, `t_alert_rules`.
2. Deploy (migration 000005 chạy lúc boot).
3. Mỗi pack: `worker -job ingest.osm -pack <slug> -dry-run` → kiểm `guard_tripped`, `closed`,
   `created` (kỳ vọng ≈ 0), `duplicate_geoms` > 0.
4. Mỗi pack: `worker -job ingest.osm -pack <slug>` để commit.
5. Kiểm lại bằng các query bằng chứng ở trên.

### Thay đổi hành vi cần bạn duyệt

- Trigger trạng thái giờ cho phép `hidden → pending` và `pending → merged` **cho mọi người**,
  kể cả admin (cần cho re-score và gộp trùng).
- Amenity giờ gắn cho cả spot `pending` (trước chỉ `active`) — khi admin duyệt lên active đã có sẵn.
- Spot `closed` không phải `closed_by_report` vẫn reopen khi content hash đổi (hành vi cũ, giữ nguyên).

### Còn lại / cần quyết định

- Lệch đồng hồ WSL: một lần đo `wall=-54s`, cùng nguyên nhân với `t_job_runs` âm — môi trường, không phải code.
- Fallback `SpotsNear` vẫn 1 query / candidate chưa match (chỉ đáng kể ở lần import đầu).
- Tx vẫn bao toàn bộ stream; tách staging ra ngoài tx nếu có pack cỡ quốc gia.
- Guard 15% với pack rất nhỏ (1–5 spot) gần như luôn trip khi có thay đổi thật → dùng `-force-guard` hoặc ngưỡng tuyệt đối tối thiểu.
- `Prepare` dọn artifact cũ bằng glob `<base>.f*` — nếu refresh và ingest cùng prepare một
  extract đồng thời, có thể xoá `.tmp` của nhau; cả hai cũng ghi chung `dest.tmp` khi tải
  (race này có từ trước). Hiếm vì refresh chạy hằng tuần; nên thêm file lock theo extract.
- Bản cũ có thể khôi phục: code backup `backend-backup-pre-ingest-fix.tgz`, DB dump `db-ingest-tables-pre-fix.dump` (scratchpad của session).
