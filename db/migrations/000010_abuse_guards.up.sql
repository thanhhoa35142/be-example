-- Indexes backing the per-device 24h rate limits (spot reports, score
-- feedback) and the "active devices per subscription" cap.
CREATE INDEX IF NOT EXISTS ix_t_spot_reports_device_created
    ON t_spot_reports (device_id, created_at);
CREATE INDEX IF NOT EXISTS ix_t_score_feedbacks_device_created
    ON t_score_feedbacks (device_id, created_at);
CREATE INDEX IF NOT EXISTS ix_t_device_subscriptions_txn
    ON t_device_subscriptions (original_transaction_id);
