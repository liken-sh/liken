# The website's image: nginx with the built site baked in.
#
# The image is deliberately dumb. Make builds the site outside
# Docker, so a local build and a CI build produce the same tree, and
# this file only copies the results. Baking the site into the image
# (rather than mounting it from a ConfigMap, as the first version of
# the website did) means one artifact carries the whole site: nested
# paths work, there is no size cap, and rolling back the site is
# running the previous tag.
FROM nginx:1.29-alpine
COPY nginx-default.conf /etc/nginx/conf.d/default.conf
COPY dist/site/ /usr/share/nginx/html/
