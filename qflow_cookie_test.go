package main

import (
	"testing"
)

func TestParseCookiesSkipsInvalidValues(t *testing.T) {
	cookies := `QF_LANG=cn; point={"url":"https://qingflow.com/"}; loginState=true; true; prodtoken=abc-123`

	got := parseCookies(cookies)
	if len(got) != 3 {
		t.Fatalf("got %d cookies, want 3: %+v", len(got), got)
	}

	want := [][2]string{
		{"QF_LANG", "cn"},
		{"loginState", "true"},
		{"prodtoken", "abc-123"},
	}
	for i, ck := range got {
		if ck.Name != want[i][0] || ck.Value != want[i][1] {
			t.Fatalf("cookie %d = %s=%s, want %s=%s", i, ck.Name, ck.Value, want[i][0], want[i][1])
		}
	}
}

func TestValidCookieValue(t *testing.T) {
	if !validCookieValue("abc-123") {
		t.Fatal("expected simple value to be valid")
	}
	if validCookieValue(`{"url":"https://qingflow.com/"}`) {
		t.Fatal("expected quoted JSON to be invalid")
	}
}
