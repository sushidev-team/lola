# Writing answers in lola

The human reads your answers in a timeline, between the commands you ran. They glance
first and dig second. Post answers with `lola ssh post narration "..."`.

## The rule

An unbroken answer over 280 characters is refused, so use real line breaks.

Lead with the verdict in one short line. Then only what they need to act. No paragraphs
that recite a command's output: they can open the command. Most answers fit in five lines.

Bad:

    GitLab docker healthy on cp-test-01 (fis-webcp-t01.cp.local). Container test-sushi-gitlab,
    image gitlab/gitlab-ce:17.11.1-ce.0, Up 8 weeks (healthy). Ports: 443, 5050, 722->22,
    8100->80, 8080->443. Services docker and gitlab-runner both active.

Good:

    GitLab is healthy
    | Container | test-sushi-gitlab |
    |---|---|
    | Image | `gitlab/gitlab-ce:17.11.1-ce.0` |
    | Uptime | 8 weeks |
    - [ok] docker active
    - [ok] gitlab-runner active
    +++ Ports
    443, 5050, 722→22, 8100→80, 8080→443
    +++

## What you can use

The first line becomes a headline when more follows it.

    **bold** and `code`              inline
    - item                           list. 1. item for ordered steps
    - [ok] up   - [warn] slow        a list item with a status mark
    - [bad] down
    | A | B |                        table. The line of dashes is required
    |---|---|
    | 1 | 2 |
    > [!OK] Backups ran last night   callout: OK, NOTE, WARN or BAD
    > [!BAD] Disk is at 100%
    +++ Full list                    fold: closed until the human opens it. Use this for
    anything long                    detail, never for the thing they asked
    +++
    ```                              code block, for output worth quoting
    df -h
    ```

Limits: 2000 characters per post. No HTML, no images, no links other than plain http(s) URLs.

## Choosing an element

    narration   your answer, or one line on what you are doing
    summary     where the investigation stands; update it with --ref
    finding     one thing you learned, with --severity and --evidence
    metric      a few key numbers, with --kv
    proposal    a command for the human to run
    question    a decision only they can make, with --choice
    widget      something text cannot show well: lola ssh guide widgets
