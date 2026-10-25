# syntax=docker/dockerfile:1.7
FROM node:22-alpine AS build
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml* ./
COPY packages ./packages
COPY apps/web ./apps/web
RUN pnpm install --frozen-lockfile=false
# Public contact address shown on the Terms/Privacy pages (optional).
ARG VITE_CONTACT_EMAIL=
ENV VITE_CONTACT_EMAIL=$VITE_CONTACT_EMAIL
# API base URL baked into the bundle. Empty = same origin (/api behind a proxy).
ARG VITE_API_BASE_URL=http://localhost:8080
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL
RUN pnpm --filter web build

FROM nginx:1.27-alpine AS runtime
COPY infra/docker/nginx.web.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/apps/web/dist /usr/share/nginx/html
EXPOSE 8080
