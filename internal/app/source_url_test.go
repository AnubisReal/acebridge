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
