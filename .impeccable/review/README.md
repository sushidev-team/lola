# Goal controls preview

These screenshots show the actual ProjectGoals component inside ProjectDetail,
rendered with local fixture data. They do not show live Linear issues, current
repository progress or a running autonomous goal.

- `goals-desktop.png`: project goal overview at 1280 × 900.
- `goals-narrow.png`: the same section at 500 × 900, without horizontal overflow.
- `goals-action.png`: complete pause review, including revision, reason and controls.

The scoped finish review cleared the goal section after fixing selection retention
and recapturing the complete action. It did not certify the full implementation
plan or exercise a live daemon. The section inherits Lola's existing tokens and
shared Button/Select components; no visual identity change was made.

## Execution settings preview

`execution-desktop.jpg` (1280 × 900) and `execution-narrow.jpg` (500 × 900)
show the actual ProjectForm Execution tab with local fixture data. They do not
demonstrate live configuration changes or authorize goal execution.

The documentation check compared ProjectForm with the incumbent app.css tokens
and shared Select, Checkbox and Button components. The tab retains the existing
modal, section hierarchy, responsive form rows and semantic colors; both captures
show complete controls without horizontal clipping. Source inspection confirms
that projects and stacks expose Execution while members do not. Goal availability
is explicitly separate from plan review, limits and execution authorization;
Docker, image and Linux Lola binary prerequisites are explained, with setup
directed to README.md, “The container sandbox”.

This is an ordinary extension of the existing system. No PRODUCT.md, DESIGN.md
or design sidecar was created. Previously noted motion-comment drift between
app.css and Button remains unrelated and unchanged. These captures and this
check do not certify the full autonomous-goal implementation or live behavior.
