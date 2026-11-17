#!/bin/sh
set -eu
mkdir -p pilot-evidence
sh /opt/pilot/setup-deps.sh > pilot-evidence/dependencies.log 2>&1
export VITEST_MAX_WORKERS=2 STORYBOOK_DISABLE_TELEMETRY=1
npm run postinstall > pilot-evidence/postinstall.log 2>&1
set +e
timeout -s TERM -k 10 180 npm run test:unit -- resources/js/components/ui/__tests__/dialog.test.tsx resources/js/components/Invoices/__tests__/save-view-dialog.test.tsx > pilot-evidence/unit.log 2>&1
unit=$?
timeout -s TERM -k 10 180 npm run test:storybook -- resources/js/components/ui/dialog.stories.tsx > pilot-evidence/story-tests.log 2>&1
stories=$?
timeout -s TERM -k 10 300 npm run build > pilot-evidence/build.log 2>&1
build=$?
timeout -s TERM -k 10 180 node /opt/pilot/capture.cjs > pilot-evidence/capture.log 2>&1
capture=$?
printf '{"unit_exit":%s,"storybook_exit":%s,"build_exit":%s,"capture_exit":%s}\n' "$unit" "$stories" "$build" "$capture" > pilot-evidence/commands.json
cat pilot-evidence/commands.json
test "$unit" = 0 && test "$stories" = 0 && test "$build" = 0 && test "$capture" = 0
