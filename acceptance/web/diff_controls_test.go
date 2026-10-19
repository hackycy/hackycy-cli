//go:build acceptance

package web

import (
	"context"
	"fmt"
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
	chromedp.ListenTarget(ctx, observer.observe)
	run := func(step string, actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, actions...); err != nil {
			var state string
			_ = chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify({highlights: window.diffHighlightCompletions, controls: [...document.querySelectorAll('[aria-label]')].map(node => node.getAttribute('aria-label')), diff: document.querySelector('diffs-container')?.shadowRoot?.textContent?.slice(-500)})`, &state))
			t.Fatalf("Diff controls (%s): %v\n%s\n%s", step, err, state, observer.describe())
		}
	}
	run("observe actual worker completions", network.Enable(), cdpruntime.Enable(), cdplog.Enable(), chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(`
			localStorage.setItem('ycy-diff-whitespace', 'true');
			localStorage.setItem('ycy-diff-wrap', 'false');
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
		chromedp.WaitVisible(`button[title="line-endings.ts"]`, chromedp.ByQuery),
		chromedp.Click(`button[title="line-endings.ts"]`, chromedp.ByQuery),
		chromedp.WaitReady(`diffs-container`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Wrap long lines"]`, chromedp.ByQuery),
		chromedp.Poll(`window.diffHighlightCompletions >= 1`, nil),
		chromedp.Click(`button[aria-label="Disable line wrapping"]`, chromedp.ByQuery),
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
	run("switch to exact comparison", chromedp.Click(`button[aria-label="Show exact diff"]`, chromedp.ByQuery), chromedp.Poll(`window.diffHighlightCompletions >= 2`, nil))
	assertDiffLines(true)
	for _, style := range []string{"Split diff", "Unified diff"} {
		run("select "+style, chromedp.Click(`button[aria-label="`+style+`"]`, chromedp.ByQuery))
		for range 2 {
			run("enable line wrapping", chromedp.Click(`button[aria-label="Wrap long lines"]`, chromedp.ByQuery), chromedp.Poll(`document.querySelector('diffs-container')?.shadowRoot?.querySelector('pre')?.dataset.overflow === 'wrap'`, nil))
			assertDiffLines(true)
			run("ignore whitespace", chromedp.Click(`button[aria-label="Ignore whitespace"]`, chromedp.ByQuery))
			assertDiffLines(false)
			run("disable line wrapping", chromedp.Click(`button[aria-label="Disable line wrapping"]`, chromedp.ByQuery))
			run("show exact diff", chromedp.Click(`button[aria-label="Show exact diff"]`, chromedp.ByQuery))
			assertDiffLines(true)
			run("reopen file using the shared cache",
				chromedp.Click(`button[aria-label="Close line-endings.ts"]`, chromedp.ByQuery),
				chromedp.WaitNotPresent(`[data-diff-editor]`, chromedp.ByQuery),
				chromedp.Click(`button[title="line-endings.ts"]`, chromedp.ByQuery),
			)
			assertDiffLines(true)
		}
	}
	for _, label := range []string{"Use dark theme", "Use light theme"} {
		run(label, chromedp.Click(`button[aria-label="`+label+`"]`, chromedp.ByQuery))
		assertDiffLines(true)
	}
	if err := observer.assertClean(pageURL, nil, []string{"/api/state", "/api/tree"}); err != nil {
		t.Fatal(err)
	}
}
