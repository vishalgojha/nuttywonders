FROM nginx:1.27-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY index.html app.js styles.css product.html product.js journal.html journal.js admin.html admin.css admin.js qr.js order.html order.js sw.js robots.txt sitemap.xml llms.txt manifest-storefront.json manifest-studio.json /usr/share/nginx/html/
COPY assets /usr/share/nginx/html/assets

EXPOSE 80
