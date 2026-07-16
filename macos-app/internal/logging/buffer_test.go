package logging

import (
	"strings"
	"testing"
)

func TestRedactsSecrets(t *testing.T) {
	b := New(5)
	b.Add("Info", "test", "password=hunter2 Authorization: BearerToken")
	got := b.Entries()[0].Message
	if strings.Contains(got, "hunter2") || strings.Contains(got, "BearerToken") {
		t.Fatalf("secret leaked: %s", got)
	}
}
func TestRedactsXMLSecrets(t *testing.T) {
	b := New(5)
	b.Add("Info", "test", `<auth><username>alice</username><password>hunter2</password><token type="bearer">verysecret</token></auth>`)
	got := b.Entries()[0].Message
	if strings.Contains(got, "hunter2") || strings.Contains(got, "verysecret") {
		t.Fatalf("XML secret leaked: %s", got)
	}
	if !strings.Contains(got, "<password>[REDACTED]</password>") || !strings.Contains(got, `<token type="bearer">[REDACTED]</token>`) {
		t.Fatalf("unexpected redaction: %s", got)
	}
}
func TestLimit(t *testing.T) {
	b := New(2)
	b.Add("Info", "test", "one")
	b.Add("Info", "test", "two")
	b.Add("Info", "test", "three")
	if got := b.Entries(); len(got) != 2 || got[0].Message != "two" {
		t.Fatalf("unexpected entries: %#v", got)
	}
}
