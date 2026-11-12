# Engineering Learnings — Pickhour

Ghi chú kinh nghiệm triển khai thực tế của dự án, để áp dụng lại cho dự án khác.
Mỗi section gồm: bài toán → cách làm → gotcha gặp phải → pattern tái sử dụng.

---

## 1. Docker packaging cho Go monolith (nhiều binary, 1 image)

### Bài toán

Một repo Go chứa nhiều entrypoint (`cmd/api`, `cmd/worker`, `cmd/mcp`,
`cmd/migrate`, `cmd/keyctl`) cần deploy thành **các container riêng biệt** —
không gom nhiều process vào 1 container.

### Cách làm

- **1 Dockerfile multi-stage** build tất cả binary vào `/out/`, runtime image
  copy nguyên thư mục → mỗi compose service chỉ khác `command:`.
- Compose: nhiều service cùng `image:` + `build: .`, phân biệt bằng `command`.

```dockerfile
RUN CGO_ENABLED=1 go build -o /out/api ./cmd/api && ... # tất cả binaries
FROM alpine:3.21
COPY --from=build /out/ /app/
CMD ["/app/api"]   # default; compose override bằng `command:`
```

```yaml
api:
  build: .
  image: pickhour-backend:latest
  command: ["/app/api"]
worker:
  image: pickhour-backend:latest
  command: ["/app/worker"]   # cùng image, khác command
```

### Gotchas đã gặp (quan trọng)

1. **`ENTRYPOINT` + compose `command` = bug ngầm.** Compose `command:` được
   truyền như *argument* cho ENTRYPOINT, không thay thế nó. Với
   `ENTRYPOINT ["/app/api"]` thì `command: ["/app/worker"]` thực chất chạy
   `/app/api /app/worker` → cả 3 service đều chạy API server. **Quy tắc:**
   binary không nhận args → dùng `CMD`, không dùng `ENTRYPOINT`.

2. **cgo quyết định runtime base image.** `github.com/uber/h3-go` bắt buộc
   cgo (wrap C library của Uber H3). Hệ quả dây chuyền:
   - Build cần `gcc musl-dev` (alpine) thay vì distroless/static.
   - Binary link động musl libc → runtime phải là alpine/musl, không thể
     distroless `static-debian12`.
   - Phải `apk add ca-certificates` vì app gọi HTTPS ra ngoài
     (Open-Meteo, APNs).
   - `CGO_ENABLED=0` local build fail với lỗi khó hiểu
     `"build constraints exclude all Go files"` — đó là dấu hiệu package
     chỉ có file `import "C"`.

3. **External network + `container_name` gây conflict.** Compose project
   name quyết định tên volume (`<project>_<volume>`). DB container tạo tay
   ngoài compose với cùng `container_name` → `docker compose up` fail
   `Conflict. The container name is already in use`. Fix: đặt `name:` top-level
   khớp project cũ → volume cũ được tái sử dụng → **không mất data**.

4. **External network không cần thiết** nếu không có service ngoài nào join.
   Bỏ `external: true` để compose tự quản network → `docker compose up` chạy
   được trên máy sạch.

5. **Healthcheck trên distroless/alpine tối giản** không có `curl`/`wget`/
   `pg_isready` sẵn trong app image → chỉ healthcheck được service có shell
   (như postgres). Với Go binary, hoặc build thêm probe binary, hoặc bỏ
   healthcheck và dựa vào `depends_on` + restart policy.

### Pattern: migration one-shot service

Thay vì mỗi service tự migrate khi boot (đua nhau, dirty-state risk), thêm
service `migrate` chạy 1 lần:

```yaml
migrate:
  command: ["/app/migrate"]
  restart: "no"
api:
  environment:
    RUN_MIGRATIONS: "false"
  depends_on:
    migrate:
      condition: service_completed_successfully
```

- Config thêm `RUN_MIGRATIONS` (default `true`) → `go run` local vẫn tiện,
  compose tắt và chờ migrate xong. Log rõ ràng: migrate `Exited (0)` trước
  khi api/worker/mcp start.

---

## 2. Scoped API keys cho machine-to-machine / agents

### Bài toán

Cho phép agent/script bên ngoài gọi API với quyền giới hạn — không cấp full
access, không share user account.

### Schema (migration `000012_api_keys`)

```sql
api_key         -- id, name, description, key_prefix, key_hash,
                -- expires_at, revoked_at, last_used_at, rate_limit_rps
api_key_grant   -- api_key_id, effect('allow'|'deny'), resource, action
```

### Nguyên tắc bảo mật

- **Không bao giờ lưu plaintext key.** Lưu `SHA-256(secret)` vào `key_hash`.
  `key_prefix` (16 ký tự đầu) chỉ để hiển thị/identify trong dashboard.
- Secret format `phk_` + 32 bytes base64url — **hiện đúng 1 lần** lúc tạo,
  sau đó không lấy lại được (mất thì revoke + tạo mới).
- `last_used_at` update **async off request path** — không block auth.

### Policy model — deny-wins, wildcard per-field

Mỗi grant là `resource:action`, mỗi field match độc lập và hỗ trợ `*`:

```
{effect: deny,  resource: "spots",        action: "delete"}  → chặn riêng lẻ
{effect: allow, resource: "spots",        action: "*"}       → spots:read+write
{effect: allow, resource: "*",            action: "read"}    → mọi read
{effect: allow, resource: "*",            action: "*"}       → full access
```

Eval: request `(resource, action)` → có deny match nào không? Có → reject.
Không → cần ≥1 allow match. Unit test bắt buộc cover: deny-wins, wildcard
từng field, không có allow → reject.

### CLI quản lý (`cmd/keyctl`)

```bash
keyctl create --name ops-agent --grant spots:read
keyctl list
keyctl grant add <id> recommendations:read
keyctl revoke <id>
```

---

## 3. MCP server cho agents (Streamable HTTP)

### Bài toán

Expose data của backend cho AI agents qua Model Context Protocol —
read-only trước, mở rộng write sau.

### Stack đã dùng

- `github.com/modelcontextprotocol/go-sdk` (official Go SDK)
- Transport: **Streamable HTTP** tại `POST /mcp` (endpoint duy nhất,
  stateless per-request)
- Auth: `Authorization: Bearer phk_...` — middleware reject 401 +
  `WWW-Authenticate` challenge khi thiếu/sai

### Nguyên tắc thiết kế

1. **Read-only tools trước.** 4 tools ban đầu:
   `spots_in_bbox`, `spot_get`, `spot_forecast`, `recommend_spots`.
   Mỗi tool map 1 scope riêng (`spots:read`, `recommendations:read`).

2. **Scope check per-tool**, không chỉ per-connection. Tool handler tự gọi
   `requireScope(ctx, "spots:read")` → grant sai trả tool error, không lộ
   data. Connection auth ≠ authorization.

3. **MCP server đọc DB trực tiếp** qua cùng sqlc repos với API — không gọi
   HTTP API của chính mình (thừa hop, thừa auth layer). Stateless nên scale
   ngang thoải mái.

4. **Output format riêng cho agents**: flat JSON tiện parse, không dùng
   GeoJSON lồng nhau của REST API.

5. **Spec khuyến nghị OAuth 2.1 cho user-facing MCP**; API key bearer là
   chuẩn cho machine/agent clients không có user identity. Khi nào có end
   user → cân nhắc OAuth.

### E2E đã verify

- `tools/list` trả đủ 4 tools; `spots_in_bbox` trả data thật từ DB
- Thiếu token → 401; key hợp lệ → `last_used_at` được stamp
- Unit test policy: deny-wins, wildcard, revoked, expired, invalid

---

## 4. Admin UI quản lý API keys (pattern SaaS/CRM)

Học từ Stripe/Vercel/GitHub PAT — áp dụng cho `pickhour-web`:

- **Bảng list**: Name+desc (truncate `max-w-52`), prefix masked
  `phk_xxx…`, grant chips (`allow`/`deny` — deny gạch đỏ), status badge
  (active/expired/revoked), **last used relative** ("3h ago" — tín hiệu
  quyết định có nên revoke không), expires + cảnh báo amber "(soon)" <5 ngày.
- **Create = dialog 2 bước**: form (name*, desc, grant editor, expiry)
  → **one-time reveal** (readonly auto-select + Copy confirm + warning
  "shown once"). Click ngoài không đóng được.
- **Grant manager per-key**: edit grants sau khi tạo (add effect
  allow/deny + resource + action, xóa từng grant) — dùng chung 2 endpoint
  `POST|DELETE /v1/admin/api-keys/{id}/grants`.
- **Revoke dialog**: nêu tên + prefix + "will stop working immediately",
  nút destructive đỏ.
- Nguyên tắc: list view **không bao giờ** show full secret; selects
  **bounded width** (`w-36`), không full-width; chips `flex-wrap`.

---

## 5. Process architecture — nhiều binary, giao tiếp qua DB

```
cmd/api     → Gin HTTP :8080        (scale ngang thoải mái)
cmd/worker  → scheduled jobs        (1 replica khuyến nghị)
cmd/mcp     → MCP Streamable HTTP   :8081/mcp
cmd/migrate → migrations 1 lần      (compose one-shot)
cmd/keyctl  → CLI quản lý keys      (không deploy)
```

- **Parallel 2 tầng**: process-level (3 services riêng, pool DB riêng) +
  goroutine-level (mỗi process tự spawn goroutines).
- **Worker an toàn multi-replica**: mỗi job giữ **Postgres advisory lock**
  (`pg_try_advisory_lock`) — replica thua lock skip; lock release khi conn
  chết → crash-safe. Schedule poll từ DB mỗi 30s → admin đổi interval
  không cần restart.
- **Workers compute ahead, API chỉ đọc** precomputed data (`spot_score`,
  `weather`, ...) — API/MCP không bao giờ gọi provider ngoài.
- **Giao tiếp gián tiếp qua PostgreSQL** — không service mesh, không
  message queue; đủ cho quy mô này.

---

## 6. Keyset/cursor pagination (admin lists)

Offset pagination chậm + skip rows khi bảng lớn → **keyset**:

```sql
-- jobs: ORDER BY id DESC, cursor = last id
WHERE (sqlc.narg('before_id')::bigint IS NULL OR id < sqlc.narg('before_id')::bigint)
ORDER BY id DESC LIMIT $1

-- reports: composite keyset (created_at, id)
WHERE (created_at, id) > (cursor_ts, cursor_id)
```

- Service fetch `limit+1` → có row thừa thì cắt, encode `nextCursor`.
- Reports cursor encode `"<unixnano>|<uuid>"` string; parse fail → 400.
- Response shape: `{data: {items, nextCursor}}`; web dùng nút "Load more"
  append (đơn giản hơn page numbers với cursor).

---

## 7. Frontend patterns đáng nhớ (pickhour-web)

- **`useSyncExternalStore` cho localStorage state** — hydration-safe,
  cross-tab sync, và **không vi phạm** `react-hooks/set-state-in-effect`
  (eslint rule bắt `setState` trong effect — bản shadcn `use-mobile.ts`
  gốc bị lỗi này, phải rewrite).
- **Pre-paint script** trong root layout đọc localStorage (UI scale,
  theme) trước first paint → không flash sai style.
- **shadcn `sidebar` component** cho sẵn: `SidebarProvider`, variant
  `sidebar|floating|inset`, `collapsible="icon"`, mobile → Sheet off-canvas,
  ⌘B toggle, cookie persistence. Nên dùng component chuẩn thay vì tự viết.
- **Persist UI prefs** qua hook `usePersistentState(key, default,
  serialize, parse)` với `useSyncExternalStore`.
- Selects luôn **bounded width**, table wrap `overflow-x-auto`,
  text dài `truncate` — admin responsive.

---

## Checklist áp dụng cho dự án mới

- [ ] Nhiều Go binaries? → 1 multi-stage Dockerfile, compose services
      khác `command`, **không ENTRYPOINT**.
- [ ] Package nào `import "C"`? → check sớm (`rg 'import "C"'`), quyết
      định CGO_ENABLED + runtime base image.
- [ ] Migrations? → one-shot `migrate` service + `RUN_MIGRATIONS` flag.
- [ ] Cần machine access? → scoped API keys: hash-only, prefix, grants
      `resource:action` allow/deny, deny-wins, `last_used_at` async.
- [ ] Agents/LLM? → MCP server read-only trước, scope per-tool, flat
      JSON output, Bearer auth.
- [ ] List lớn? → keyset pagination `limit+1` + `nextCursor`.
- [ ] React state từ localStorage? → `useSyncExternalStore`, không
      setState-in-effect.
