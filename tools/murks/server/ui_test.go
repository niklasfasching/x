package server

import (
	"regexp"
	"strings"
	"testing"

	"github.com/niklasfasching/x/headless"
	"github.com/niklasfasching/x/snap"
	"github.com/niklasfasching/x/sq"
)

func TestDashboardUI(t *testing.T) {
	h, s := NewHarness(t, true), snap.New(t)
	h.Login(t, "user")
	h.Exec("Browser.grantPermissions", headless.Params{
		"origin": h.URL(""),
		"permissions": []string{
			"clipboardReadWrite",
			"clipboardSanitizedWrite",
		},
	}, nil)
	h.MockPrompt("My App")
	h.Wait(`$("button[title='New App']")`, 5)
	h.Eval(`$("button[title='New App']").click()`)
	h.Wait(`!!$("button[title='Copy Message']")`, 5)
	h.Eval(`$("button[title='Copy Message']").click()`)
	prompt := h.Wait(`await navigator.clipboard.readText()`, 5).(string)
	prompt = regexp.MustCompile(`"PAT_[^"]+"`).ReplaceAllString(prompt, `"PAT"`)
	s.KeyedSnap(t, "new app prompt", strings.NewReplacer(h.URL(""), DevDomain).Replace(prompt))
	x, err := sq.QueryOne[App](h.DB, "SELECT * FROM apps WHERE Name = ? AND Owner = ?", "My App", "user")
	if err != nil {
		t.Fatalf("failed to find created app: %v", err)
	}
	appSlug := x.Slug
	if x.IsPublic {
		t.Fatalf("expected app to be private initially")
	}
	if x.Status != StatusDraft {
		t.Fatalf("expected new app to have Status=Draft (0), got %d", x.Status)
	}
	x.Status = StatusDev
	h.apps.Update(x, "Status")
	h.Eval("location.reload()")
	h.Wait(`!!$("button[title='Make Public']")`, 5)
	h.Wait(`!!document.querySelector(".entry.under-construction")`, 5)
	entryHTML := h.Eval(`document.querySelector(".entry").outerHTML`).(string)
	entryHTML = strings.NewReplacer(h.URL(""), DevDomain, x.ID, "APP_ID").Replace(entryHTML)
	entryHTML = regexp.MustCompile(`:\d{4,5}`).ReplaceAllString(entryHTML, "")
	s.KeyedSnap(t, "app entry construction", entryHTML)
	h.MockConfirm(true)
	h.Eval(`document.querySelector("button[title*='Construction']").click()`)
	h.Wait(`!document.querySelector(".entry.under-construction")`, 5)
	x, err = sq.QueryOne[App](h.DB, "SELECT ID, Status FROM apps WHERE ID = ?", x.ID)
	if err != nil {
		t.Fatalf("failed to query app: %v", err)
	}
	if x.Status != StatusLive {
		t.Fatalf("expected Status=Live (2) after toggle, got %d", x.Status)
	}
	entryHTML = h.Eval(`document.querySelector(".entry").outerHTML`).(string)
	entryHTML = strings.NewReplacer(h.URL(""), DevDomain, x.ID, "APP_ID").Replace(entryHTML)
	entryHTML = regexp.MustCompile(`:\d{4,5}`).ReplaceAllString(entryHTML, "")
	s.KeyedSnap(t, "app entry live", entryHTML)
	h.MockConfirm(true)
	h.Eval(`document.querySelector("button[title*='Live']").click()`)
	h.Wait(`!!document.querySelector(".entry.under-construction")`, 5)
	x, err = sq.QueryOne[App](h.DB, "SELECT ID, Status FROM apps WHERE ID = ?", x.ID)
	if err != nil {
		t.Fatalf("failed to query app: %v", err)
	}
	if x.Status != StatusDev {
		t.Fatalf("expected Status=Dev (1) after second toggle, got %d", x.Status)
	}
	h.MockConfirm(true)
	h.Eval(`$("button[title='Make Public']").click()`)
	h.Wait(`!$("button[title='Make Public']")`, 5)
	x, err = sq.QueryOne[App](h.DB, "SELECT ID, IsPublic FROM apps WHERE ID = ?", x.ID)
	if err != nil {
		t.Fatalf("failed to query app: %v", err)
	}
	if !x.IsPublic {
		t.Fatal("expected app to be public")
	}

	h.Eval(`$("button[title='Options']").click()`)
	h.Wait(`$("#app-sheet")?.classList.contains('open')`, 5)
	h.Eval(`$("button[title='Copy Prompt and HTML']").click()`)
	clipboard := h.Wait(`await navigator.clipboard.readText()`, 5).(string)
	if !strings.Contains(clipboard, "import { API } from") {
		t.Fatalf("expected clipboard to contain 'import', got %q", clipboard)
	}
	h.Eval(`await navigator.clipboard.writeText("")`)
	h.Eval(`$("button[title='Options']").click()`)
	h.Wait(`$("#app-sheet")?.classList.contains('open')`, 5)
	h.Eval(`$("button[title='Copy Invite Link']").click()`)
	clipboard = h.Wait(`await navigator.clipboard.readText()`, 5).(string)
	if !strings.Contains(clipboard, "/invite?code=") {
		t.Fatalf("expected clipboard to contain invite link, got %q", clipboard)
	}
	h.Eval(`$("button[title='Options']").click()`)
	h.Wait(`$("#app-sheet")?.classList.contains('open')`, 5)
	h.MockConfirm(true)
	h.Eval(`$("button[title='Delete App']").click()`)
	h.Wait(`location.pathname === "/"`, 5)
	h.Wait(`!document.body.innerText.includes("My App")`, 5)
	x, err = sq.QueryOne[App](h.DB, "SELECT * FROM apps WHERE Slug = ?", appSlug)
	if err == nil {
		t.Fatal("expected app to be deleted")
	}
}
