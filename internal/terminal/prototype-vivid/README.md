# Vivid Terminal Prototype

> THROWAWAY PROTOTYPE. This is visual-decision evidence, not production code.

This PTY prototype compares three structurally different treatments of the
same configure-profile journey:

- `console`: the default dense operator-oriented Ops Console with a command
  bar, metadata row, `STATE / PHASE / DETAIL` table, and active content area;
- `signal`: a persistent left-hand workflow rail (comparison only);
- `focus`: one centered decision or active phase at a time.

Run it from the repository root:

```sh
make prototype-terminal
```

The default is the B Ops Console success journey. It uses amber as the primary
color, cyan as the accent, symbol-paired status labels, and a bottom Huh focus
rule without a persistent left rail. On narrow terminals the table and active
area collapse to one column. The bottom switcher uses F2 and F3 for the
previous/next variant, F4 for success/failure/cancellation, and F5 to restart.
The same state can be launched directly:

```sh
make prototype-terminal PROTOTYPE_ARGS='--variant=console --outcome=failure'
make prototype-terminal PROTOTYPE_ARGS='--variant=focus --outcome=cancel'
```

`--variant=b` is an alias for `console`; `--variant=a|signal` and
`--variant=c|focus` remain comparison keys.

All values are in memory. The example credential is synthetic and every
transcript renders it as `[redacted]`.
