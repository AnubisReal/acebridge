package app

import "testing"

func TestNormalizeInBrowserIPNSURL(t *testing.T) {
	input := "https://k2k4r8lm8tkmuxbc8lkmq1in3v0oya1p6pe9o5bu0hu30br5ko08k2gb.ipns.inbrowser.link/data/listas/listaplana.txt"
	want := "https://ipfs.io/ipns/k2k4r8lm8tkmuxbc8lkmq1in3v0oya1p6pe9o5bu0hu30br5ko08k2gb/data/listas/listaplana.txt"
	got, err := normalizeSourceFetchURL(input)
	if err != nil || got != want {
		t.Fatalf("got %q (%v), want %q", got, err, want)
	}
}

func TestNormalizeRegularSourceURLUnchanged(t *testing.T) {
	input := "https://example.com/channels.m3u?version=2"
	got, err := normalizeSourceFetchURL(input)
	if err != nil || got != input {
		t.Fatalf("got %q (%v), want unchanged", got, err)
	}
}

func TestNormalizeSourceProxyURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"192.168.0.70:1080", "socks5://192.168.0.70:1080"},
		{"socks5://warp.local:1080", "socks5://warp.local:1080"},
		{"http://proxy.local:3128", "http://proxy.local:3128"},
		{"", ""},
	}
	for _, test := range tests {
		got, err := normalizeSourceProxyURL(test.input)
		if err != nil || got != test.want {
			t.Fatalf("normalizeSourceProxyURL(%q) = %q, %v", test.input, got, err)
		}
	}
	if _, err := normalizeSourceProxyURL("ftp://proxy.local:21"); err == nil {
		t.Fatal("unsupported proxy scheme was accepted")
	}
}
