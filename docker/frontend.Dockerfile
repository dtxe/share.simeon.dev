FROM node:22-alpine AS base
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install

FROM base AS dev
COPY frontend/ .
EXPOSE 5173
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0"]

FROM base AS build
COPY frontend/ .
RUN npm run build

FROM nginx:1.27-alpine AS prod
COPY docker/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
