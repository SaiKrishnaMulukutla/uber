FROM nginx:alpine

COPY nginx.conf /etc/nginx/nginx.conf.template

RUN apk add --no-cache gettext \
    && mkdir -p /var/cache/nginx/client_temp \
                /var/cache/nginx/proxy_temp \
                /var/cache/nginx/fastcgi_temp \
                /var/cache/nginx/uwsgi_temp \
                /var/cache/nginx/scgi_temp \
    && chown -R nginx:nginx /var/cache/nginx \
    && touch /var/run/nginx.pid \
    && chown nginx:nginx /var/run/nginx.pid

EXPOSE 8000

# Extract the system DNS server at runtime, inject into nginx.conf, then start.
CMD ["sh", "-c", "RESOLVER=$(awk '/^nameserver/{print $2; exit}' /etc/resolv.conf) envsubst '${RESOLVER}' < /etc/nginx/nginx.conf.template > /etc/nginx/nginx.conf && nginx -g 'daemon off;'"]
