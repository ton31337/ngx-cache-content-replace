## Replace Nginx's cache content (body)

### Usage

#### Extract Nginx cache body (PNG image)

```
% sudo ./main --cache-file /var/lib/cache/b/5cfd0dc58ca95cfee8ce72ceeadee21b --extract-to-file /home/donatas/1.png
Content-Type: image/png
Content-Length: 254057
```

#### Convert PNG to WEBP

```
% cwebp /home/donatas/1.png -o /home/donatas/1.webp
Saving file '/home/donatas/1.webp'
File:      /home/donatas/1.png
Dimension: 600 x 600
Output:    9162 bytes Y-U-V-All-PSNR 43.71 45.95 44.93   44.21 dB
block count:  intra4: 809
              intra16: 635  (-> 43.98%)
              skipped block: 437 (30.26%)
bytes used:  header:            111  (1.2%)
             mode-partition:   2468  (26.9%)
 Residuals bytes  |segment 1|segment 2|segment 3|segment 4|  total
    macroblocks:  |       2%|       8%|      20%|      68%|    1444
      quantizer:  |      36 |      36 |      29 |      23 |
   filter level:  |      11 |      15 |      38 |      25 |
```

#### Replace an existing Nginx cache body with the data file (PNG -> WEBP)

```
% sudo ./main --cache-file /var/lib/cache/b/5cfd0dc58ca95cfee8ce72ceeadee21b --data-file /home/donatas/1.webp
Cache key length: 102
Cache key: 0-http://cdn.example.com/wp-content/uploads/2022/12/product-1671457294-520209193-600x600.png
Headers length: 530
Headers start: 439
Headers: map[Accept-Ranges:[bytes] Age:[84802] Cache-Control:[public, max-age=31557600] Connection:[keep-alive] Content-Length:[9162] Content-Type:[image/webp] Date:[Wed, 08 Mar 2023 12:01:13 GMT] Etag:["3e069-63be9b54-af2449b61a2ebb13;;;"] Expires:[Wed, 06 Mar 2024 18:27:51 GMT] Last-Modified:[Wed, 11 Jan 2023 11:19:48 GMT] Platform:[hostinger] Server:[hcdn] X-Hcdn-Cache-Edge:[edge2] X-Hcdn-Cache-Status:[HIT] X-Hcdn-Info:[rtt:42579 crh:0.001] X-Hcdn-Request-Id:[b34765e1763e86eaf13df80318c9bf6a-fast-edge1] X-Turbo-Charged-By:[LiteSpeed]]
Body start: 988
```
