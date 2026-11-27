package netguard

import "testing"

func TestCheckURL(t *testing.T) {
	Configure(false)
	defer Configure(false)
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"https://api.openai.com/v1/chat/completions", false},
		{"http://api.openai.com/v1/chat/completions", true},
		{"http://127.0.0.1:8080/v1/chat/completions", false},
		{"http://localhost:8788/v1/models", false},
		{"http://192.168.1.10:8080/v1", false},
		{"http://10.0.0.5/v1", false},
		{"ftp://example.com/file", true},
	}
	for _, c := range cases {
		if err := CheckURL(c.url); (err != nil) != c.wantErr {
			t.Fatalf("CheckURL(%q) err=%v, wantErr=%v", c.url, err, c.wantErr)
		}
	}
}

func TestCheckURLAllowed(t *testing.T) {
	Configure(true)
	defer Configure(false)
	if err := CheckURL("http://api.example.com/v1"); err != nil {
		t.Fatalf("显式放行后仍被拒绝: %v", err)
	}
}

func TestCheckFetchURL(t *testing.T) {
	if err := CheckFetchURL("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Fatal("应拒绝云元数据地址")
	}
	if err := CheckFetchURL("https://example.com/a.png"); err != nil {
		t.Fatalf("正常图片地址被拒绝: %v", err)
	}
}
