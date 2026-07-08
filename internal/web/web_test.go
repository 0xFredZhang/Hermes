package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewRendererParsesAllPages(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	for _, name := range []string{"login", "accounts", "projects", "blueprints", "environments", "environment_detail"} {
		if r.pages[name] == nil {
			t.Fatalf("page %q not parsed", name)
		}
	}
}

func TestRenderPartialEnvStatusUp(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Env":       map[string]any{"ID": int64(1), "Status": "up", "Outputs": map[string]any{"public_ips": []any{"1.2.3.4"}}},
		"PublicIPs": "1.2.3.4",
	}
	if err := r.RenderPartial(&b, "env_status", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "销毁") || !strings.Contains(out, "1.2.3.4") {
		t.Fatalf("env_status(up) missing destroy/ip: %s", out)
	}
}
