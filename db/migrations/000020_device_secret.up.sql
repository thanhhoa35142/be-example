-- Per-device bearer secret. The raw secret is shown to the app once at
-- registration and kept in the iOS Keychain; only its SHA-256 hash is stored.
-- NULL means "legacy row, no credential yet" — the next registration issues
-- one. Cleared by SoftDeleteDevice ("Delete my data").
ALTER TABLE t_devices ADD COLUMN secret_hash text;
