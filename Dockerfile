FROM node:24-alpine
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY server.js ./
COPY src ./src
COPY public ./public
ENV HOST=0.0.0.0 PORT=3000 STATE_DIR=/data
VOLUME /data
EXPOSE 3000
CMD ["node", "server.js"]
