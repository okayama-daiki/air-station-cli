package airstation

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestNormalizeMAC(t *testing.T) {
	if got := NormalizeMAC("aa-bb-cc-dd-ee-ff"); got != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("NormalizeMAC() = %q", got)
	}
}

func TestIsIPv4(t *testing.T) {
	if !IsIPv4("192.168.11.1") {
		t.Fatal("expected valid IPv4")
	}
	if IsIPv4("999.1.1.1") {
		t.Fatal("unexpected valid IPv4")
	}
}

func TestEncodeURIComponent(t *testing.T) {
	got := encodeURIComponent("a b+c@")
	want := "a%20b%2Bc%40"
	if got != want {
		t.Fatalf("encodeURIComponent() = %q, want %q", got, want)
	}
}

func TestWrap64(t *testing.T) {
	input := strings.Repeat("A", 70)
	want := strings.Repeat("A", 64) + "\n" + strings.Repeat("A", 6)
	if got := wrap64(input); got != want {
		t.Fatalf("wrap64() = %q, want %q", got, want)
	}
}

func TestFormValues(t *testing.T) {
	html := `
	<form method="post" action="/submit">
		<input type="hidden" name="token" value="abc">
		<input type="checkbox" name="macmode_11bg" value="1" checked>
		<input type="checkbox" name="macmode_11a" value="1">
		<textarea name="maclist">AA:BB:CC:DD:EE:FF</textarea>
		<input type="submit" name="ADD" value="add">
	</form>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	form, err := findForm(doc, func(form *Form) bool {
		return form.HasControl("token") && form.HasControl("ADD")
	})
	if err != nil {
		t.Fatal(err)
	}

	values := form.Values("ADD")
	if values.Get("token") != "abc" {
		t.Fatalf("token = %q", values.Get("token"))
	}
	if values.Get("macmode_11bg") != "1" {
		t.Fatalf("macmode_11bg = %q", values.Get("macmode_11bg"))
	}
	if values.Get("macmode_11a") != "" {
		t.Fatalf("macmode_11a = %q", values.Get("macmode_11a"))
	}
	if values.Get("maclist") != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("maclist = %q", values.Get("maclist"))
	}
	if values.Get("ADD") != "add" {
		t.Fatalf("ADD = %q", values.Get("ADD"))
	}
}
