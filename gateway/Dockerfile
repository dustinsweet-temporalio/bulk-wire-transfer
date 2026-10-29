# Gateway image: builds the FI portal (ui/) and the Demo Control Center (control-ui/) with Node LTS, then
# serves both from nginx with three server blocks (see nginx.conf). Build context is the repository root.

FROM node:26.13.0-alpine3.24 AS portal
WORKDIR /app
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM node:26.13.0-alpine3.24 AS control
WORKDIR /app
COPY control-ui/package.json control-ui/package-lock.json ./
RUN npm ci
COPY control-ui/ ./
RUN npm run build

FROM nginx:1.30.6-alpine
COPY gateway/nginx.conf /etc/nginx/nginx.conf
COPY --from=portal /app/dist /usr/share/nginx/portal
COPY --from=control /app/dist /usr/share/nginx/control
EXPOSE 3001 3002 3100
