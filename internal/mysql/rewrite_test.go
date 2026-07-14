package mysqlutil

import (
	"strings"
	"testing"
)

func TestRewriteCreateSQL(t *testing.T) {
	in := "CREATE TABLE `notification_logs` (\n  `id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB AUTO_INCREMENT=5 DEFAULT CHARSET=utf8mb4"
	out := rewriteCreateSQL(in, "archive", "notification_logs")
	if !strings.HasPrefix(out, "CREATE TABLE `archive`.`notification_logs`") {
		t.Fatalf("got %q", out)
	}
	if strings.Contains(strings.ToUpper(out), "AUTO_INCREMENT=") {
		t.Fatalf("expected AUTO_INCREMENT= stripped: %q", out)
	}
}
