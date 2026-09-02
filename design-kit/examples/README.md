# examples/

## `theme.css`

The shipping form of this design system for the source Go app: `tokens.css` +
`colors_and_type.css` + `components.css` concatenated in load order, ready to drop at
`internal/web/static/theme.css`. One file because the app serves static CSS with no
build step, and because the CSP forbids inline styles — every value has to be here.

Regenerate after any token or component change:

```sh
cat tokens.css colors_and_type.css components.css > examples/theme.css
```

## Preserved source examples

The source project handed over exactly two files — `tokens.css` and `DESIGN.md` — and
no implementation or artifact code. Both originals are preserved byte-for-byte in
`context/source-tokens.css` and `context/source-DESIGN.md`; the working `tokens.css`
at the package root is the same file with the motion and ramp-slot blocks appended.

There is therefore no source HTML, template, or component implementation to preserve
here. The closest thing to a source component is the prose specification in
`DESIGN.md` §5, which `components.css` implements clause by clause. Everything under
`ui_kits/app/` is written from that specification, not copied from a running page.
