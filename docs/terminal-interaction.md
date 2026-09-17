# Terminal scrolling

The interactive console keeps the command/status bar and keyboard hints fixed.
The content between them scrolls continuously. Long metadata, phase details,
notices, options, and results wrap within the window instead of being discarded.
Earlier notices remain available for the lifetime of the console.

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
