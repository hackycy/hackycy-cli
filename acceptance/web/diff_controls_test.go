//go:build acceptance

package web

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func assertDiffControls(t *testing.T, pageURL string) {
	t.Helper()
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options, chromedp.ExecPath(chromeExecutable(t)))
	allocator, stopAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	defer stopAllocator()
	browser, stopBrowser := chromedp.NewContext(allocator)
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(browser, 45*time.Second)
	defer cancel()
	observer := newBrowserObserver()
	var refreshing atomic.Bool
	// A published refresh expires the previous snapshot. Requests racing its
	// event may return 409; their handlers must recover without an exception.
	snapshotRequest := func(rawURL string) bool {
		return strings.Contains(rawURL, "snapshot=") && (strings.HasPrefix(rawURL, pageURL+"/api/tree?") || strings.HasPrefix(rawURL, pageURL+"/api/entries/"))
	}
	chromedp.ListenTarget(ctx, func(event any) {
		if refreshing.Load() {
			switch value := event.(type) {
			case *network.EventResponseReceived:
				if value.Response != nil && value.Response.Status == 409 && snapshotRequest(value.Response.URL) {
					return
				}
			case *cdplog.EventEntryAdded:
				if value.Entry != nil && strings.Contains(value.Entry.Text, "409 (Conflict)") && snapshotRequest(value.Entry.URL) {
					return
				}
			}
		}
		observer.observe(event)
	})
	run := func(step string, actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, actions...); err != nil {
			var state string
			_ = chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify({highlights: window.diffHighlightCompletions, counts: document.querySelector('.summary-bar')?.textContent, beforeRefresh: [window.beforeRefreshModified, window.beforeRefreshUnchanged], controls: [...document.querySelectorAll('[aria-label]')].map(node => node.getAttribute('aria-label')), diff: document.querySelector('diffs-container')?.shadowRoot?.textContent?.slice(-500)})`, &state))
			t.Fatalf("Diff controls (%s): %v\n%s\n%s", step, err, state, observer.describe())
		}
	}
	run("observe actual worker completions", network.Enable(), cdpruntime.Enable(), cdplog.Enable(), chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(`
			localStorage.setItem('ycy-diff-whitespace', 'true');
			localStorage.setItem('ycy-diff-wrap', 'false');
			if (localStorage.getItem('ycy-diff-line-endings') === null)
				localStorage.setItem('ycy-diff-line-endings', 'false');
			window.diffHighlightCompletions = 0;
			const NativeWorker = window.Worker;
			window.Worker = class extends NativeWorker {
				constructor(...args) {
					super(...args);
					this.addEventListener('message', ({ data }) => {
						if (data.type === 'success' && data.requestType === 'diff')
							window.diffHighlightCompletions++;
					});
				}
			};
		`).Do(ctx)
		return err
	}))
	run("open LF/CRLF file with whitespace ignored",
		emulation.SetDeviceMetricsOverride(1280, 800, 1, false),
		chromedp.Navigate(pageURL),
		chromedp.WaitVisible(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
		chromedp.Click(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
		chromedp.WaitReady(`diffs-container`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Wrap lines"][aria-pressed="false"]`, chromedp.ByQuery),
		chromedp.Poll(`window.diffHighlightCompletions >= 1`, nil),
		chromedp.Click(`button[aria-label="Wrap lines"][aria-pressed="true"]`, chromedp.ByQuery),
	)
	assertDiffLines := func(exact bool) {
		t.Helper()
		expected := 0
		if exact {
			expected = 4
		}
		run("check rendered lines", chromedp.Poll(fmt.Sprintf(`(() => {
			const root = document.querySelector('[data-diff-editor] diffs-container')?.shadowRoot;
			if (!root) return false;
			const lines = [...root.querySelectorAll('[data-code] [data-line]')];
			if (lines.length !== %d) return false;
			return lines.every(line => ["const value = 'same';", "export { value };"]
				.includes(line.textContent.trim()));
		})()`, expected), nil))
	}
	assertDiffLines(false)
	// The empty ignored diff must reach the shared worker cache before exact
	// mode is opened. Waiting for worker completion avoids a timing-only repro.
	run("switch to exact comparison", chromedp.Click(`button[aria-label="Ignore whitespace"][aria-pressed="true"]`, chromedp.ByQuery), chromedp.Poll(`window.diffHighlightCompletions >= 2`, nil))
	assertDiffLines(true)
	for _, style := range []string{"Side-by-side diff", "Unified diff"} {
		run("select "+style, chromedp.Click(`button[aria-label="`+style+`"]`, chromedp.ByQuery))
		for range 2 {
			run("enable line wrapping", chromedp.Click(`button[aria-label="Wrap lines"][aria-pressed="false"]`, chromedp.ByQuery), chromedp.Poll(`document.querySelector('diffs-container')?.shadowRoot?.querySelector('pre')?.dataset.overflow === 'wrap'`, nil))
			assertDiffLines(true)
			run("ignore whitespace", chromedp.Click(`button[aria-label="Ignore whitespace"][aria-pressed="false"]`, chromedp.ByQuery))
			assertDiffLines(false)
			run("disable line wrapping", chromedp.Click(`button[aria-label="Wrap lines"][aria-pressed="true"]`, chromedp.ByQuery))
			run("show exact diff", chromedp.Click(`button[aria-label="Ignore whitespace"][aria-pressed="true"]`, chromedp.ByQuery))
			assertDiffLines(true)
			run("reopen file using the shared cache",
				chromedp.Click(`button[aria-label="Close line-endings.ts"]`, chromedp.ByQuery),
				chromedp.WaitNotPresent(`[data-diff-editor]`, chromedp.ByQuery),
				chromedp.Click(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
			)
			assertDiffLines(true)
		}
	}
	for _, wasDark := range []bool{false, true} {
		run("toggle dark mode", chromedp.Click(fmt.Sprintf(`button[aria-label="Dark mode"][aria-pressed="%t"]`, wasDark), chromedp.ByQuery))
		assertDiffLines(true)
	}
	run("record exact counts", chromedp.Evaluate(`window.rawModifiedCount = Number(document.querySelector('.summary-modified span:last-child').textContent); window.rawUnchangedCount = Number(document.querySelector('.summary-unchanged span:last-child').textContent);`, nil))
	run("ignore only LF/CRLF",
		chromedp.Click(`button[aria-label="Ignore line endings"][aria-pressed="false"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-diff-status-bar] [aria-label="Status: Unchanged"]`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-diff-editor]').textContent.includes('No differences when ignoring LF/CRLF')`, nil),
		chromedp.Poll(`Number(document.querySelector('.summary-modified span:last-child').textContent) === window.rawModifiedCount - 1 && Number(document.querySelector('.summary-unchanged span:last-child').textContent) === window.rawUnchangedCount + 1`, nil),
	)
	run("filter Unchanged while preserving its open tab",
		chromedp.Click(`.summary-unchanged`, chromedp.ByQuery),
		chromedp.WaitNotPresent(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`button[aria-label="Close line-endings.ts"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-diff-status-bar] [aria-label="Status: Unchanged"]`, chromedp.ByQuery),
	)
	run("search uses the same effective status",
		chromedp.SendKeys(`input[aria-label="Search files or folders"]`, "line-endings", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('aside').textContent.includes('No matching files or folders')`, nil),
		chromedp.Click(`.summary-unchanged`, chromedp.ByQuery),
		chromedp.WaitVisible(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
	)
	// A separate browser has separate preferences and observes the raw status.
	if err := func() error {
		otherAllocator, stopOtherAllocator := chromedp.NewExecAllocator(context.Background(), options...)
		defer stopOtherAllocator()
		otherBrowser, closeOtherBrowser := chromedp.NewContext(otherAllocator)
		defer closeOtherBrowser()
		otherContext, cancelOther := context.WithTimeout(otherBrowser, 15*time.Second)
		defer cancelOther()
		return chromedp.Run(otherContext,
			emulation.SetDeviceMetricsOverride(1280, 800, 1, false),
			chromedp.Navigate(pageURL),
			chromedp.WaitVisible(`button[aria-label="Ignore line endings"][aria-pressed="false"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
			chromedp.Click(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-diff-status-bar] [aria-label="Status: Modified"]`, chromedp.ByQuery),
		)
	}(); err != nil {
		t.Fatalf("independent browser view: %v", err)
	}
	run("persist line-ending preference on reload", chromedp.Reload(),
		chromedp.WaitVisible(`button[aria-label="Ignore line endings"][aria-pressed="true"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
		chromedp.Click(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-diff-status-bar] [aria-label="Status: Unchanged"]`, chromedp.ByQuery),
	)
	refreshing.Store(true)
	run("switch comparison modes during refresh",
		chromedp.Evaluate(`window.beforeRefreshModified = Number(document.querySelector('.summary-modified span:last-child').textContent); window.beforeRefreshUnchanged = Number(document.querySelector('.summary-unchanged span:last-child').textContent);`, nil),
		chromedp.Click(`button[aria-label="Refresh comparison"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Ignore line endings"][aria-pressed="true"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Ignore line endings"][aria-pressed="false"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Ignore line endings"][aria-pressed="true"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Ignore line endings"][aria-pressed="false"]`, chromedp.ByQuery),
		chromedp.WaitNotPresent(`button[aria-label="Close line-endings.ts"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`button[aria-label="Refresh comparison"]`, chromedp.ByQuery),
		chromedp.Click(`aside button[title="line-endings.ts"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-diff-status-bar] [aria-label="Status: Unchanged"]`, chromedp.ByQuery),
		chromedp.Poll(`Number(document.querySelector('.summary-modified span:last-child').textContent) === window.beforeRefreshModified && Number(document.querySelector('.summary-unchanged span:last-child').textContent) === window.beforeRefreshUnchanged`, nil),
	)
	refreshing.Store(false)
	run("restore exact status and content",
		chromedp.Click(`button[aria-label="Ignore line endings"][aria-pressed="true"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Ignore whitespace"][aria-pressed="true"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-diff-status-bar] [aria-label="Status: Modified"]`, chromedp.ByQuery),
	)
	assertDiffLines(true)
	run("narrow screen keeps the control reachable", emulation.SetDeviceMetricsOverride(390, 844, 1, true),
		chromedp.WaitVisible(`button[aria-label="Ignore line endings"]`, chromedp.ByQuery),
		chromedp.Poll(`(() => { const rect = document.querySelector('button[aria-label="Ignore line endings"]').getBoundingClientRect(); return rect.x >= 0 && rect.right <= innerWidth; })()`, nil),
	)
	if err := observer.assertClean(pageURL, nil, []string{"/api/state", "/api/tree"}); err != nil {
		t.Fatal(err)
	}
}
