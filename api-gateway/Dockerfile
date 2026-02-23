FROM nginx:1.25-alpine
LABEL maintainer="ride-hailing-system"

# Remove default config then copy ours
RUN rm /etc/nginx/conf.d/default.conf
COPY nginx.conf /etc/nginx/nginx.conf

# Expose gateway port
EXPOSE 8000

# Use the built-in nginx entrypoint
CMD ["nginx", "-g", "daemon off;"]
