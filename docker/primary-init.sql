-- Sample schema + data for local demo (primary).
CREATE TABLE IF NOT EXISTS notification_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  status VARCHAR(32) NOT NULL,
  body TEXT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  KEY idx_notification_logs_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS audit_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  event_type VARCHAR(64) NOT NULL,
  payload JSON NULL,
  event_at DATETIME(6) NOT NULL,
  KEY idx_audit_events_event_at (event_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Expired rows (older than 90d / 180d relative to ~2026-07-14)
INSERT INTO notification_logs (id, status, body, created_at) VALUES
  (1, 'sent', 'old-1', '2025-01-01 00:00:00.000000'),
  (2, 'failed', 'old-2', '2025-06-01 00:00:00.000000'),
  (3, 'pending', 'old-pending-not-moved', '2025-01-01 00:00:00.000000'),
  (4, 'sent', 'recent', '2026-07-01 00:00:00.000000');

INSERT INTO audit_events (id, event_type, payload, event_at) VALUES
  (1, 'login', JSON_OBJECT('u', 1), '2025-01-01 00:00:00.000000'),
  (2, 'logout', JSON_OBJECT('u', 1), '2026-07-01 00:00:00.000000');
