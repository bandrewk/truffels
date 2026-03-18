-- Trend alert default settings
INSERT OR IGNORE INTO admin_settings (key, value) VALUES ('trend_alert_enabled', 'true');
INSERT OR IGNORE INTO admin_settings (key, value) VALUES ('trend_alert_horizon_hours', '6');
INSERT OR IGNORE INTO admin_settings (key, value) VALUES ('trend_alert_lookback_hours', '6');
INSERT OR IGNORE INTO admin_settings (key, value) VALUES ('trend_alert_min_data_hours', '2');
