# Surface: Claude in Chrome

The extension runs in the user's own Chromium, so the Fiken session is already logged in.
Load the tools once: `tabs_context_mcp`, `navigate`, `get_page_text`, `javascript_tool`.

- Work in your own tab group. The user's tabs are invisible to you; open the panel URL
  yourself with `navigate`, do not ask for their tab.
- Read with `javascript_tool` and the primitives in
  [`../reference/superforing-dom.md`](../reference/superforing-dom.md). `get_page_text` is
  enough for a first look but lacks `linje.id`. Screenshots are for a final sanity check only.
- Each `javascript_tool` call is capped near 45 s. Confirm a handful of lines per call and
  re-scan; the loops are resumable because they read live state.
- After any API write that should change a suggestion, `navigate` to the same URL again.
  The panel does not re-evaluate on its own.
- A 404 page makes `find` and `read_page` hang. Keep to the URL format in the DOM
  reference; the path is `/bank/bkbf/`, not `/bankavstemming`.
