# Proposal: weather refresh bằng Open-Meteo tự host trên GitHub Actions

Ngày: 2026-11-15. Trạng thái: đề xuất, chưa làm. Liên quan: `AUDIT-FINDINGS.md` #11, #12, #5.

## 1. Vấn đề

- `weather.refresh` gọi `api.open-meteo.com` (free tier). Free tier chỉ cho phép dùng phi thương mại; app có subscription (Plus) bị coi là thương mại. Từ ngày bán Plus, free tier không còn hợp lệ ở bất kỳ tần suất nào.
- Về lượng gọi: `picnic` có 1013 weather cell, lịch hiện tại 900 s, giới hạn 9000 call/ngày. Quota hết sau khoảng 8 lượt (~2 giờ), phần còn lại của ngày dữ liệu cũ mà job vẫn báo "succeeded".
- Chủ dự án chưa có doanh thu nên không mua gói trả phí, và không muốn tốn ổ cứng server cho dữ liệu thời tiết.

## 2. Giải pháp

Chạy Open-Meteo tự host **bên trong một job GitHub Actions theo lịch**. Runner tự làm hết rồi tắt; không ai gọi vào runner.

Mỗi lượt chạy:

1. Khởi động container Open-Meteo (`ghcr.io/open-meteo/open-meteo`) trên runner.
2. Sync đúng các biến cần dùng từ kho open-data của Open-Meteo.
3. Vào Tailscale để nối tới Postgres.
4. Chạy binary worker với `-job weather.refresh`, URL Open-Meteo trỏ vào `localhost`.
5. Kết thúc. Worker trên server vẫn chạy `scoring.recompute` theo lịch.

Cơ sở pháp lý: code Open-Meteo là AGPLv3 (chỉ ràng buộc khi sửa mã nguồn, ta chạy nguyên bản), dữ liệu là CC BY 4.0 (phải ghi nguồn trong app). Hạn chế phi thương mại chỉ áp dụng cho server do Open-Meteo vận hành.

Không làm runner thành server cho worker gọi vào: điều khoản GitHub Actions không cho dùng làm hạ tầng phục vụ request, và lịch Actions hay trễ nên hai bên dễ trượt giờ.

## 3. Phase 0: đo trước, quyết định sau (bắt buộc làm đầu tiên)

Chưa viết code sản phẩm. Tạo một workflow thử (`workflow_dispatch`) chỉ khởi động container và sync, rồi ghi lại:

| Cần đo / xác minh | Điều kiện đi tiếp |
|---|---|
| Mô hình nào cung cấp đủ 6 biến: `temperature_2m`, `apparent_temperature`, `precipitation_probability`, `wind_speed_10m`, `uv_index`, `cloud_cover` | Cả 6 biến trả về giá trị khác null cho vài cell thử |
| Độ dài dự báo của mô hình đó | Đủ 8 ngày (xem ghi chú bên dưới) |
| Dung lượng đĩa sau khi sync | Vừa đĩa trống của runner, còn dư ít nhất 20% |
| Thời gian một lượt (sync + 1013 call cục bộ) | Tính được số phút/tháng, xem mục 6 |
| Tên lệnh và tham số sync đúng | Lấy từ `--help` của image chính thức, không chép từ blog |

Ghi chú về độ dài dự báo: `weather.refresh` đang tải 15 ngày (`weather/service.go:14`), nhưng sản phẩm chỉ dùng 7 ngày: chấm điểm (`scoring/service.go:11`), API forecast (`spots/service.go:15`), app ("Next 7 days") và gói Plus ("7 days"). Chủ dự án đã đồng ý rút xuống 8 ngày (7 ngày dùng thật + 1 ngày dự phòng cho lệch múi giờ). Vì vậy chỉ cần sync đủ 8 ngày, không cần mô hình 15 ngày.

Chưa xác minh, đừng coi là sự thật: `precipitation_probability` và `uv_index` lấy từ mô hình nào khi tự host; dung lượng thật; đĩa trống của runner (ước chừng 14 GB).

So sánh chất lượng: với 5–10 cell, so kết quả tự host với `api.open-meteo.com` cùng thời điểm. Lệch lớn thì báo lại trước khi đi tiếp.

**Nếu Phase 0 không đạt:** dừng, báo số đo, chuyển sang phương án dự phòng ở mục 8.

### Kết quả Phase 0 (2026-11-15): ĐẠT, đi tiếp có điều kiện

Số đo của worker (3 lượt trên Actions): sync 4 domain (`copernicus_dem90`, `ncep_gfs013`, `ncep_gefs025`, `ncep_gefs05`), 18.4 GB, sync ~50 giây, cả lượt ~7 phút, 14/14 cell đủ 6 biến trong 16 ngày. Phải query với `models=gfs_global`; `best_match` không dùng được khi tự host.

Ba việc phải trả lời trong lúc làm Phase 1, trước khi bật lịch:

1. **So với production thật, không phải so với chính nó.** Độ lệch 0.000 là so cùng mô hình. Production hiện gọi không có `models=` (tức `best_match`, có trộn mô hình khu vực độ phân giải cao). Đo lại trên cùng 14 cell: tự host `gfs_global` so với API công khai `best_match`, báo độ lệch nhiệt độ, xác suất mưa, và điểm số cuối cùng sau khi chấm.
2. **Làm rõ phát hiện "precipitation_probability = 0 đúng như production".** Xác nhận production hôm nay có nhận xác suất mưa thật hay đang nhận 0. Nếu đang nhận 0 thì ngưỡng mưa trong scoring chưa từng hoạt động, và đó là một lỗi riêng cần báo.
3. **Đĩa trống của runner.** Báo dung lượng trống trước và sau sync. Cần dư ít nhất 20%.

Điều chỉnh theo số đo:

- **Tần suất: 4 lượt/ngày là tối ưu, không phải thỏa hiệp.** GFS chỉ ra bản mới 4 lần/ngày (00, 06, 12, 18 UTC), chạy dày hơn không có dữ liệu mới. Đặt cron lệch sau mỗi bản GFS đủ lâu để dữ liệu đã có trên kho; worker đo độ trễ thật rồi chọn giờ.
- **Chỉ cần 8 ngày**, nên `ncep_gefs05` (chỉ cần cho ngày 11–16) có thể bỏ nếu `gfs_global` vẫn trả đủ 8 ngày khi thiếu nó. Thử một lượt để xác nhận; không bắt buộc.
- **Ghi nguồn:** thêm NOAA/NCEP (GFS, GEFS) và Copernicus DEM bên cạnh Open-Meteo. Kiểm tra điều khoản ghi nguồn của Copernicus DEM GLO-90.
- **Trước go-live chỉ chạy bằng `workflow_dispatch` vào DB thử.** Chưa bật `schedule` và chưa ghi vào `picnic` (theo `CLAUDE.md`: worker tắt cho tới go-live).

## 4. Phase 1: thay đổi code (nhỏ)

0. **Thêm `models=gfs_global` vào query** trong `openmeteo.go`, cấu hình được qua biến môi trường (ví dụ `OPEN_METEO_MODELS`, mặc định rỗng để giữ hành vi cũ với API công khai).

1. **URL cấu hình được.** `internal/modules/weather/openmeteo.go:14` đang là hằng số cứng. Thêm `OPEN_METEO_URL` vào `internal/platform/config`, mặc định giữ nguyên URL hiện tại, truyền vào `NewOpenMeteo`.
2. **Job phải báo lỗi thật (#12).** `weather.Refresh` trả lỗi khi quota bị bỏ qua hoặc tỉ lệ cell lỗi vượt ngưỡng (đề xuất 20%), để lượt Actions hiện đỏ.
3. **`ReplaceRows` an toàn (#5).** Xóa và ghi trong một transaction; không xóa khi provider trả về 0 dòng. Với nguồn tự host mới, lỗi cấu hình sync rất dễ cho ra kết quả rỗng, nên việc này phải xong trước khi bật lịch.
4. **Giảm `ForecastDays` của weather từ 15 xuống 8** (`weather/service.go:14`). Trước khi đổi, grep lại để chắc không còn chỗ nào đọc forecast quá 7 ngày (crowd, alerts, digest). Các dòng đã lưu cho ngày 9–15 không cần migration: chúng không được đọc, và sẽ bị các lượt refresh sau ghi đè khi rơi vào cửa sổ 8 ngày.
5. **Ghi nguồn dữ liệu.** Thêm dòng attribution Open-Meteo (CC BY 4.0) vào app và trang web nếu chưa có.

Mỗi thay đổi có test hồi quy. Test tích hợp dùng `TEST_DATABASE_URL` (`picnic_test`), không dùng `picnic`.

## 5. Phase 2: workflow chính thức

File: `pickhour-backend/.github/workflows/weather-refresh.yml`, theo mẫu workflow `ingest-cloud` đang có.

- Trigger: `schedule` + `workflow_dispatch`. `concurrency` một nhóm, không hủy lượt đang chạy.
- `timeout-minutes` đặt bằng khoảng 2 lần thời gian đo ở Phase 0.
- Biến môi trường cho bước chạy worker:
  - `OPEN_METEO_URL=http://localhost:<port>/v1/forecast`
  - `WEATHER_DAILY_LIMIT=0` (tự host không có quota)
  - `RUN_MIGRATIONS=false`
  - Không đặt `REDIS_URL`: bỏ bump cache version, chấp nhận API trễ tối đa bằng TTL cache (10 phút).
- **Tắt lịch `weather.refresh` trên server** (`t_worker_job_schedules.enabled = false`), nếu không worker server vẫn gọi `api.open-meteo.com`.

Bảo mật:

- Khóa Tailscale loại ephemeral, có tag riêng, ACL chỉ cho tới cổng Postgres.
- Tài khoản DB riêng cho job này, quyền tối thiểu: đọc `t_spots`, ghi bảng weather và `t_provider_quotas`, ghi `t_job_runs`, đọc bảng lịch job. Worker liệt kê chính xác quyền cần từ code rồi mới tạo role.
- Mọi bí mật nằm trong GitHub Secrets; không in `DATABASE_URL` ra log.

## 6. Tần suất

Chọn sau khi có số đo ở Phase 0:

- Repo public: không giới hạn phút, có thể chạy mỗi 1–3 giờ.
- Repo private: giới hạn phút miễn phí hằng tháng (kiểm tra con số trên trang GitHub của tài khoản). Ví dụ mỗi lượt 10 phút: 4 lượt/ngày là khoảng 1200 phút/tháng, mỗi giờ là khoảng 7200 phút/tháng.

Đề xuất khởi đầu: 4 lượt/ngày (mỗi 6 giờ). Lịch Actions có thể trễ hoặc bỏ lượt, nên không thiết kế gì phụ thuộc vào đúng giờ.

## 7. Tiêu chí hoàn thành

- Một lượt Actions chạy xong ghi forecast cho toàn bộ cell vào DB thử, không gọi ra `api.open-meteo.com`.
- Lượt lỗi (sync hỏng, DB không tới được, nhiều cell lỗi) hiện đỏ trên Actions và ghi `failed` vào `t_job_runs`.
- Dữ liệu forecast cũ không bị xóa khi lượt mới lỗi hoặc trả về rỗng.
- `scoring.recompute` trên server dùng được dữ liệu mới mà không cần đổi code.
- Báo cáo kèm số đo: dung lượng, thời gian mỗi lượt, mô hình đã chọn, độ lệch so với API gốc.

## 8. Phương án dự phòng

Nếu Phase 0 không đạt (thiếu biến, quá đĩa, quá chậm): viết provider MET Norway (`api.met.no`, Locationforecast) sau interface `weather.Provider`. Miễn phí cho cả mục đích thương mại theo CC BY 4.0; cần `User-Agent` có thông tin liên hệ, tôn trọng cache header, ghi nguồn. Phải kiểm tra trước: độ dài dự báo, độ phân giải theo giờ ở các ngày sau, và các trường có sẵn toàn cầu.

## 9. Ngoài phạm vi và ràng buộc

- Không mua gói trả phí, không dựng Open-Meteo thường trực trên server.
- Trước go-live: worker server vẫn tắt, không refresh điểm hằng ngày (theo `CLAUDE.md`). Thử nghiệm ghi vào DB thử, không ghi vào `picnic`.
- Cho tới go-live vẫn được dùng free tier `api.open-meteo.com` ở chu kỳ 12 giờ để phát triển.
- Worker báo lại sau Phase 0 trước khi làm Phase 1 và 2.
