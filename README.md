## Replace Nginx's cache content (body)

### Usage

#### Extract Nginx cache body (PNG image)

```
./main --cache-file /var/lib/cache/b/5cfd0dc58ca95cfee8ce72ceeadee21b --extract-to-file /home/donatas/1.png
```

#### Convert PNG to WEBP

```
cwebp /home/donatas/1.png -o /home/donatas/1.webp
```

#### Replace an existing Nginx cache body with the data file (PNG -> WEBP)

```
./main --cache-file /var/lib/cache/b/5cfd0dc58ca95cfee8ce72ceeadee21b --data-file /home/donatas/1.webp
```
