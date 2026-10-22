FROM node:24-bookworm-slim@sha256:55fb5f5a527ea2c5c4a95f963e48ef90aa38c7b346054fdc8ee9957a2b55d205
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/playwright
RUN apt-get update && apt-get install -y --no-install-recommends git ripgrep ca-certificates python3 procps && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/nori
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
RUN ./node_modules/.bin/playwright install --with-deps chromium
RUN npm install -g --no-audit --no-fund @anthropic-ai/claude-code@2.1.307
COPY vendor ./vendor
RUN chmod -R a+rX /opt/nori /opt/playwright
COPY capture.cjs setup-deps.sh check.sh /opt/pilot/
ENV HOME=/tmp/home
USER 501:20
WORKDIR /tmp
