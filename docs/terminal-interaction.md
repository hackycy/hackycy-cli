# Terminal presentation and scrolling

The interactive console uses a flush-left Focus Flow layout with no outer or
form indentation and no maximum content width. Titles, selection markers,
inputs, confirmation buttons, and validation errors all begin at the terminal's
first column. Its mint primary and violet focus colors are shared with rich
results and transcript headings. The existing dark palette policy applies;
with `NO_COLOR`, the focused confirmation choice uses ASCII brackets such as
`[Yes]  No` so focus never depends on color alone.

The fixed application shell separates command identity, progress, and active
content. Its first row keeps the emphasized command and muted target on the left
and the current status against the right edge. A full-width divider follows,
then the neighboring-step trail and one fixed blank row before the scrollable
body. The trail is the only live progress overview and shows up to three steps
from the current form or work catalog. Completed, active, and pending steps use
`✓`, `◆`, and `○`, joined by a quiet horizontal rule. On narrow screens the trail
drops neighboring steps before abbreviating the current name. A console without
a catalog, and a completed outcome, has no trail but retains the divider and
body spacing.

The complete visual baseline is an `80x24` terminal. Smaller supported windows
remain operable, bounded, and scrollable, but do not compress the normal desktop
spacing. The scrollable body contains metadata, notices, the current form or
work phase, and the result summary; completed history is printed in the final
transcript after terminal restoration. Active work uses a pulse animation.

The content between them scrolls continuously. Long metadata, phase details,
notices, options, and results wrap within the window instead of being discarded.
Earlier notices remain available for the lifetime of the console.

The pager line appears only when content overflows, following is paused, or new
content is unread. Its `wheel` hint appears only while overflow exists and mouse
mode is active. The `Ctrl+G` mouse/copy toggle stays in the compact help line.

| Context | Controls |
| --- | --- |
| Any scrollable content | Mouse wheel or trackpad wheel events |
| Work, notices, results | Up/Down scroll one line; Home/End go to the start/end |
| Lists and forms | Alt+Up/Down scroll without changing the selection or input |
| Lists | Up/Down move the selection; `/` filters; Enter confirms |
| Multiple selection | Space toggles an item; Ctrl+A toggles all |
| Text selection/copy | Ctrl+G releases mouse capture; use the terminal's normal copy gesture; Ctrl+G restores wheel input |

Scrolling a list does not change its selection. If the selected item is outside
the viewport, the first Enter reveals it and the second confirms it. Typing or
moving the selection brings the focused control back into view. Input editing
keys retain their normal meaning.

New notices follow the bottom by default. Scrolling upward pauses following and
shows the number of new logical lines. Scroll back to the bottom or press End to
resume. Resizing preserves the reading position as closely as possible. Below
30 columns or 10 rows, a size warning replaces the view while interaction state
is retained; Escape and Ctrl+C remain available for cancellation.

Short outcomes retain the automatic closing delay. Outcomes whose summary cannot
fit remain open for scrolling; Enter or Escape closes them. Scrolling any outcome
also keeps it open until dismissed. Results still go to stdout after terminal
restoration, and the bounded transcript explicitly marks truncation as before.
If a task completes while you are reading earlier notices, the reading position
is retained and the console waits for you to close it.

Wheel input depends on the terminal forwarding mouse events. Keyboard scrolling
works without mouse support. Trackpad scrolling uses the terminal's wheel events,
not pixel-level scrolling. Mouse reporting is disabled on exit and while copy
mode is active.
