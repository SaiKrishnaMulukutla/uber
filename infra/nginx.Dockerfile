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

CMD ["sh", "-c", "envsubst '${USER_SERVICE_URL} ${DRIVER_SERVICE_URL} ${TRIP_SERVICE_URL} ${NOTIFICATION_SERVICE_URL} ${PAYMENT_SERVICE_URL} ${USER_SERVICE_ADDR} ${DRIVER_SERVICE_ADDR} ${TRIP_SERVICE_ADDR} ${NOTIFICATION_SERVICE_ADDR} ${PAYMENT_SERVICE_ADDR}' < /etc/nginx/nginx.conf.template > /etc/nginx/nginx.conf && nginx -g 'daemon off;'"]
