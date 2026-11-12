# Working Agreement — Pickhour Backend

Cách chúng ta làm việc trên repo này, để session sau (hoặc người/agent
khác) tiếp tục đúng convention mà không phải hỏi lại.

## Repos

Ba repo **độc lập hoàn toàn**, không submodule:

| Repo | Nội dung |
|---|---|
| `pickhour-app` | iOS SwiftUI app (build trên macOS CI, không có Xcode local) |
| `pickhour-backend` | repo này — Go API/worker/MCP + Postgres |
| `pickhour-web` | Next.js admin (shadcn/ui) |

## Branch & CI

- Làm việc trên nhánh **`dev`**, push thẳng lên `dev`.
- CI **chỉ chạy trên `main`**: push main + PR vào main. Push dev không tốn
  CI minutes. Merge dev → main qua PR khi muốn verify.

## Local dev — QUAN TRỌNG

- **Không build Docker image.** Dev chạy trực tiếp:
  ```bash
  go run ./cmd/api      # :8080
  go run ./cmd/worker   # scheduled jobs
  go run ./cmd/mcp      # :8081/mcp
  go run ./cmd/keyctl   # CLI quản lý API keys
  ```
- Postgres/PostGIS chạy qua docker compose **chỉ phần db**
  (`docker compose up -d db`) hoặc container `picnic-postgres` sẵn có.
  Compose file có đủ api/worker/mcp/migrate services cho deploy sau này,
  nhưng **không rebuild/up chúng trong dev**.
- `RUN_MIGRATIONS` default `true` → mỗi binary tự migrate khi boot, tiện
  cho `go run`. Compose set `false` + one-shot `migrate` service chỉ dành
  cho deploy.

## Database — schema CHƯA chốt

- **Chủ dự án sẽ viết migration và gộp lại sau.** Schema hiện tại là
  evolving — đừng coi là final.
- Tránh tạo bảng mới trừ khi thật cần (như `provider_quota` ở
  000013 — nếu thấy không cần thiết có thể gộp/bỏ khi consolidate).
- Mỗi thay đổi DB bắt buộc theo đúng flow:
  1. Sửa/thêm `db/migrations/NNNNNN_name.up.sql` + `.down.sql`
     (số tiếp theo hiện tại: **000013**)
  2. Sửa `db/queries/*.sql` — **không bao giờ sửa tay file generated**
  3. `make sqlc` regenerate
  4. Verify `go build ./... && go test ./...`
- `sqlc.arg(name)` để đặt tên param rõ; `limit` là reserved word của
  Postgres — tránh dùng làm tên param.
- sqlc đôi khi infer sai nullable → dùng `sqlc.narg()` cho param nullable.

## Kiến trúc — rules không được phá

- `internal/modules/<domain>` sở hữu entity + repository interface +
  service; `internal/infrastructure/persistence` implement repo qua sqlc;
  `internal/infrastructure/http` Gin router; `internal/platform` shared
  (config, log, jobs, httpx).
- **Workers compute ahead, API/MCP chỉ đọc** dữ liệu precomputed —
  không gọi provider ngoài trong request path.
- Worker jobs giữ **Postgres advisory lock** → an toàn multi-replica.
- API response **camelCase**; server trả reason codes + params, client
  tự localize — không bao giờ gửi text đã dịch.
- **Không lưu user location**; không log API keys/tokens/secrets;
  log token chỉ `prefix(8)…`.
- DB access **chỉ qua sqlc** — không raw query trong service layer.

## Rate limiting & provider calls (đã implement ở 000013+)

- `internal/platform/httpx` — RoundTripper: token bucket per provider +
  retry (429/503/network, full-jitter, tôn trọng `Retry-After` pause cả
  provider). Provider clients nhận `*http.Client` qua constructor.
- `provider_quota` table — atomic daily budget per provider
  (`ConsumeProviderQuota`), weather refresh consume cả batch trước khi
  fan-out. Env: `WEATHER_DAILY_LIMIT` (default 9000), `WEATHER_H3_RES`
  (default 6), `WEATHER_PROVIDER` (open-meteo|weatherkit).
- Weather cells dùng **H3 res thô hơn spot cells** — `geo.WeatherCellID`
  map res7→res6 trước khi fetch/lookup. Đừng join weather_hourly bằng
  spot.cell_id trực tiếp.
- `errgroup.SetLimit(8)` cho fan-out; per-cell lỗi → `Failed++` continue,
  không abort job.
- MCP inbound: per-key token bucket từ `rate_limit_rps`, 429 +
  `Retry-After` + problem+json.
- Jobs có `initialDelay` stagger first run (weather 0, alerts 1m,
  crowd.aggregate 2m, scoring 4m, crowd.refresh 7m).

## Verify trước khi push

```bash
cd pickhour-backend
make sqlc      # nếu đụng db/queries hoặc migrations
go build ./... && go vet ./... && gofmt -l .
go test ./...
```

E2E nhanh: chạy `go run ./cmd/api` + `go run ./cmd/worker`, check
`job_run` table và logs.

## Docs

- `docs/API.md` + `docs/openapi.yaml` — API contract
- `docs/LEARNINGS.md` — bài học kỹ thuật chi tiết (Docker gotchas, API
  keys, MCP, iOS patterns) để tái sử dụng cho dự án khác
- File này — cách làm việc hàng ngày
