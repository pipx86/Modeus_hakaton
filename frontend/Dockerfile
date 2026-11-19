# syntax=docker/dockerfile:1.7
# Frontend build + nginx runtime. The build context is the repository root so
# the Dockerfile can reuse frontend/package-lock.json; deploy/nginx is excluded
# from the root context, therefore the nginx configs live under frontend/nginx.

FROM node:22-alpine AS build
WORKDIR /app

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY frontend/nginx/default.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
