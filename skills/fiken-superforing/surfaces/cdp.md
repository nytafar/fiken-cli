# Surface: CDP

Status: planned. The primitives in
[`../reference/superforing-dom.md`](../reference/superforing-dom.md) are the contract; the
script will evaluate them over Chrome DevTools Protocol against a Chromium started with
`--remote-debugging-port=9222` (the user's session, already logged in), or headless.

Until the script lands, use [`claude-in-chrome.md`](claude-in-chrome.md). The prior
exploration notes live in `docs/browser-workflows.md` at the repo root.
