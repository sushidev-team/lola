#!/bin/sh
set -eu
# Vite resolves package sources through realpath; keep its dependencies inside
# the isolated workspace so its ordinary filesystem allowlist remains valid.
if [ ! -e node_modules ]; then cp -a /opt/nori/node_modules ./node_modules; fi
if [ ! -e vendor ]; then cp -a /opt/nori/vendor ./vendor; fi
npm run postinstall
