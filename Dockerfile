FROM nginx:1.27-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY index.html app.js styles.css admin.html admin.css admin.js qr.js order.html order.js sw.js manifest-storefront.json manifest-studio.json /usr/share/nginx/html/
COPY assets /usr/share/nginx/html/assets

EXPOSE 80
