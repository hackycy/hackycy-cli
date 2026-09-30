//go:build acceptance

package web

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func assertTunnelDialogScrolling(t *testing.T, pageURL string, session *http.Cookie, clientID string) {
	t.Helper()
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options, chromedp.ExecPath(chromeExecutable(t)))
	allocator, stopAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	defer stopAllocator()
	browser, stopBrowser := chromedp.NewContext(allocator)
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(browser, 120*time.Second)
	defer cancel()
	run := func(step string, actions ...chromedp.Action) {
		t.Helper()
		t.Logf("dialog scroll: %s", step)
		if err := chromedp.Run(ctx, actions...); err != nil {
			t.Fatalf("Tunnel dialog scroll workflow (%s): %v", step, err)
		}
	}
	run("set session cookie", network.SetCookie(session.Name, session.Value).WithURL(pageURL).WithHTTPOnly(session.HttpOnly).WithSameSite(network.CookieSameSiteStrict))
	for _, viewport := range []struct{ width, height int64 }{{1280, 800}, {1280, 360}, {390, 844}, {320, 700}, {390, 360}} {
		step := fmt.Sprintf("open account dialog at %dx%d", viewport.width, viewport.height)
		run(step+": set viewport", emulation.SetDeviceMetricsOverride(viewport.width, viewport.height, 1, false))
		run(step+": navigate", chromedp.Navigate(pageURL+"/accounts"))
		run(step+": wait for account page", chromedp.Sleep(time.Second), chromedp.WaitVisible(`//button[normalize-space()="Create account"]`, chromedp.BySearch))
		run(step+": click create account", chromedp.Click(`//button[normalize-space()="Create account"]`, chromedp.BySearch))
		run(step+": wait for account dialog", chromedp.WaitVisible(`.modal-body-viewport`, chromedp.ByQuery))
		assertDialogScrollRegions(t, ctx, ".modal-body-viewport", viewport.height == 360)
		if viewport.height == 800 {
			var height float64
			run("measure short account dialog", chromedp.Evaluate(`document.querySelector('.modal').getBoundingClientRect().height`, &height))
			if height >= 500 {
				t.Fatalf("short account dialog expanded to %v pixels", height)
			}
		}
		run("close account dialog with Escape", chromedp.SendKeys(`.modal input`, kb.Escape, chromedp.ByQuery), chromedp.WaitNotPresent(`.modal`, chromedp.ByQuery))
	}
	run(
		"open tunnel editor",
		chromedp.Navigate(pageURL+"/clients/"+clientID),
		chromedp.WaitVisible(`//button[normalize-space()="New tunnel"]`, chromedp.BySearch),
		chromedp.Click(`//button[normalize-space()="New tunnel"]`, chromedp.BySearch),
		chromedp.WaitVisible(`.form-scroll-viewport`, chromedp.ByQuery),
	)
	for index := range 16 {
		run(fmt.Sprintf("add custom domain %d", index+1), chromedp.Click(`//button[normalize-space()="Add custom domains"]`, chromedp.BySearch))
	}
	for _, theme := range []string{"light", "dark"} {
		run("set "+theme+" theme", chromedp.Evaluate(fmt.Sprintf(`document.documentElement.dataset.adminTheme = %q`, theme), nil))
		for _, viewport := range []struct{ width, height int64 }{{1280, 800}, {390, 844}, {320, 360}} {
			run(fmt.Sprintf("inspect tunnel editor at %dx%d in %s theme", viewport.width, viewport.height, theme), emulation.SetDeviceMetricsOverride(viewport.width, viewport.height, 1, false))
			assertDialogScrollRegions(t, ctx, ".form-scroll-viewport", true)
			run(
				"submit invalid tunnel editor at "+fmt.Sprintf("%dx%d in %s theme", viewport.width, viewport.height, theme),
				chromedp.Click(`.modal button[type="submit"]`, chromedp.ByQuery),
				chromedp.Poll(`document.activeElement?.name === 'customDomains.0.value'`, nil),
			)
			var focused bool
			run("check validation focus at "+fmt.Sprintf("%dx%d in %s theme", viewport.width, viewport.height, theme), chromedp.Evaluate(`(() => {
				const field = document.activeElement.getBoundingClientRect();
				const viewport = document.querySelector('.form-scroll-viewport').getBoundingClientRect();
				return field.top >= viewport.top && field.bottom <= viewport.bottom;
			})()`, &focused))
			if !focused {
				t.Fatalf("validation focus was clipped at %dx%d in %s theme", viewport.width, viewport.height, theme)
			}
		}
	}
	run(
		"close tunnel editor",
		chromedp.Click(`//div[@role="dialog"]//button[normalize-space()="Cancel"]`, chromedp.BySearch),
		chromedp.WaitNotPresent(`.modal`, chromedp.ByQuery),
	)
	var pointerEvents string
	run("check pointer events", chromedp.Evaluate(`document.body.style.pointerEvents`, &pointerEvents))
	if pointerEvents != "" {
		t.Fatalf("closing scrolling dialog left pointer events locked: %q", pointerEvents)
	}
}

func assertDialogScrollRegions(t *testing.T, ctx context.Context, selector string, mustScroll bool) {
	t.Helper()
	if err := chromedp.Run(ctx, chromedp.Poll(`document.querySelector('.modal').getAnimations().every(animation => animation.playState === 'finished')`, nil)); err != nil {
		t.Fatalf("wait for dialog opening animation: %v", err)
	}
	var result struct {
		Height        float64 `json:"height"`
		Overflow      bool    `json:"overflow"`
		ChromeVisible bool    `json:"chromeVisible"`
		RegionsFixed  bool    `json:"regionsFixed"`
		ReachedEnd    bool    `json:"reachedEnd"`
		Scrollable    bool    `json:"scrollable"`
	}
	expression := fmt.Sprintf(`(() => {
		const viewport = document.querySelector(%q);
		const title = document.querySelector('.modal-title');
		const footer = document.querySelector('.modal-footer');
		const titleTop = title.getBoundingClientRect().top;
		const footerTop = footer.getBoundingClientRect().top;
		viewport.scrollTop = viewport.scrollHeight;
		return {
			height: viewport.clientHeight,
			overflow: document.documentElement.scrollWidth > innerWidth + 1,
			chromeVisible: titleTop >= -1 && footer.getBoundingClientRect().bottom <= innerHeight + 1,
			regionsFixed: Math.abs(title.getBoundingClientRect().top - titleTop) <= 1 && Math.abs(footer.getBoundingClientRect().top - footerTop) <= 1,
			reachedEnd: Math.abs(viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop) <= 1,
			scrollable: viewport.scrollHeight > viewport.clientHeight + 1
		};
	})()`, selector)
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &result)); err != nil {
		t.Fatalf("inspect %s: %v", selector, err)
	}
	if result.Height <= 0 || result.Overflow || !result.ChromeVisible || !result.RegionsFixed || !result.ReachedEnd || (mustScroll && !result.Scrollable) {
		t.Fatalf("dialog scrolling %s: %+v", selector, result)
	}
}
