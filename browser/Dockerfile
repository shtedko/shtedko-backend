# Browser runs only inside the Compose network; it is never published to Windows.
FROM mcr.microsoft.com/playwright:v1.56.0-noble
WORKDIR /srv/browser
COPY package.json ./
RUN npm install --omit=dev --no-audit --no-fund
COPY server.mjs ./
ENV PORT=3333
EXPOSE 3333
CMD ["node", "server.mjs"]
