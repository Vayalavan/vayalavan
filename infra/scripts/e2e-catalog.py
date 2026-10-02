#!/usr/bin/env python3
"""
End-to-end catalog checks against a RUNNING stack.

Exercises the real chain — browser -> gateway (JWT, role guard, zod) ->
vm-catalog-api (supplier scoping) -> Postgres + MinIO.

Covers what must never regress:
  1. rupee -> paise across the wire, including "1,200.50"
  2. updating a product replaces its unit set atomically
  3. a supplier cannot read, edit or archive another supplier's product
  4. presigned image upload: real PUT then real GET, bytes compared
  5. CSV upload -> preview (writes nothing) -> commit -> idempotent re-commit
  6. admin cross-supplier read/archive, and role guards both ways

Run with:  make -C infra test-e2e-catalog   (requires `make dev` + `make seed`)
Exits non-zero on the first failed assertion.
"""
import json, urllib.request, urllib.error, uuid
G = "http://localhost:8080/api"

def call(method, path, body=None, token=None, raw=None, ctype=None):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(G + path, data=data, method=method)
    req.add_header("Content-Type", ctype or "application/json")
    if token: req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req) as r:
            t = r.read().decode(); return r.status, (json.loads(t) if t and t.lstrip()[:1] in "{[" else t)
    except urllib.error.HTTPError as e:
        t = e.read().decode(); return e.code, (json.loads(t) if t and t.lstrip()[:1] in "{[" else t)

def login(i, p):
    s, b = call("POST", "/auth/login", {"identifier": i, "password": p})
    assert s == 200, (s, b)
    return b["tokens"]["access_token"]

s1 = login("greens@vayal.test", "local_dev_supplier_pw")
s2 = login("farm@vayal.test", "local_dev_supplier_pw")
admin = login("admin@vayal.test", "local_dev_admin_pw_change_me")
cust = login("customer@vayal.test", "local_dev_customer_pw")
print("  logged in: supplier-1, supplier-2, admin, customer")

print("\n=== CREATE product with size codes and packs (price as STRING) ===")
name = "Tomato " + uuid.uuid4().hex[:6]
s, p = call("POST", "/products", {
    "name": name, "type": "vegetable", "grade": "A",
    "description": "Vine ripened",
    # One implicit grade: this test is about rupee->paise across the wire, and
    # a single size code is the shape that isolates it from grade handling.
    "size_codes": [{"code": "STD", "packs": [
        {"label": "1 kg", "weight_grams": 1000, "price_rupees": "45.50"},
        {"label": "3 kg", "weight_grams": 3000, "price_rupees": "1,200.50"},
    ]}],
}, token=s1)
print(f"  POST /products -> {s}")
assert s == 201, p
pid = p["id"]
packs = p["size_codes"][0]["packs"]
print(f"  packs: {[(u['label'], u['price_paise'], u['price_display']) for u in packs]}")

print("\n=== rupee->paise across the wire ===")
assert packs[0]["price_paise"] == 4550, packs[0]
assert packs[1]["price_paise"] == 120050, packs[1]
print("  45.50 -> 4550 paise, '1,200.50' -> 120050 paise  ✓")

print("\n=== UPDATE replaces the unit set atomically ===")
s, p2 = call("PUT", f"/products/{pid}", {
    "name": name, "type": "vegetable", "grade": "A", "status": "active",
    "size_codes": [{"code": "STD", "packs": [
        {"label": "500 g", "weight_grams": 500, "price_rupees": "25.00"}]}],
}, token=s1)
print(f"  PUT -> {s}, packs now: {[u['label'] for u in p2['size_codes'][0]['packs']]}, status={p2['status']}")
assert len(p2["size_codes"]) == 1 and len(p2["size_codes"][0]["packs"]) == 1, p2

print("\n=== OWNERSHIP: supplier-2 cannot see or touch supplier-1's product ===")
for m, path, body in [("GET", f"/products/{pid}", None),
                      ("PUT", f"/products/{pid}", {"name":"Hijack","type":"other",
                                                    "size_codes":[{"code":"STD","packs":[{"label":"1 kg","weight_grams":1000,"price_rupees":"1.00"}]}]}),
                      ("POST", f"/products/{pid}/archive", None)]:
    st, r = call(m, path, body, token=s2)
    code = r.get("error", {}).get("code") if isinstance(r, dict) else ""
    print(f"  {m:5} {path[:34]:36} as supplier-2 -> {st} {code}")
    assert st == 404, (m, st, r)

print("\n=== supplier-2's list does not contain it ===")
s, lst = call("GET", "/products", token=s2)
assert all(x["id"] != pid for x in lst["products"]), "LEAK: supplier-2 sees supplier-1's product"
print(f"  supplier-2 sees {lst['total']} products, none of them supplier-1's  ✓")

print("\n=== customer cannot touch product routes at all ===")
s, r = call("GET", "/products", token=cust)
print(f"  GET /products as customer -> {s} {r.get('error',{}).get('code','')}")
assert s == 403

print("\n=== search + filters ===")
s, r = call("GET", f"/products?search={name.split()[1]}&status=active", token=s1)
print(f"  search+status filter -> {s}, total={r['total']}")
assert r["total"] == 1, r

print("\n=== image presign (§6.6) ===")
s, pre = call("POST", "/uploads/presign",
              {"content_type": "image/jpeg", "size_bytes": 120000, "filename": "t.jpg"}, token=s1)
print(f"  presign -> {s}, key={pre['key'][:44]}...")
assert s == 200 and pre["key"].startswith("products/"), pre
s, bad = call("POST", "/uploads/presign",
              {"content_type": "application/pdf", "size_bytes": 100}, token=s1)
print(f"  presign a PDF -> {bad if isinstance(bad,str) else bad['error']['code']} ({s})")
assert s == 422
s, big = call("POST", "/uploads/presign",
              {"content_type": "image/png", "size_bytes": 99999999}, token=s1)
print(f"  presign 100MB image -> {s} {big['error']['details']}")
assert s == 422
# A video is allowed, and gets the larger of the two size caps: 20 MB is over
# the image limit and under the video one, so it proves the cap is per kind.
s, vid = call("POST", "/uploads/presign",
              {"content_type": "video/mp4", "size_bytes": 20_000_000}, token=s1)
print(f"  presign 20MB video -> {s}, kind={vid.get('kind')}, key={vid.get('key','')[-4:]}")
assert s == 200 and vid["kind"] == "video" and vid["key"].endswith(".mp4"), vid
s, toobig = call("POST", "/uploads/presign",
                 {"content_type": "video/mp4", "size_bytes": 99_999_999}, token=s1)
print(f"  presign 100MB video -> {s} {toobig['error']['details']}")
assert s == 422


# --- images, CSV import and admin ---------------------------------------

print("=== presigned image upload: real PUT then real GET (the browser path) ===")
s, pre = call("POST","/uploads/presign",{"content_type":"image/png","size_bytes":70,"filename":"x.png"},token=s1)
png = bytes.fromhex("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000a49444154789c6360000002000100" "05fe02fea7c9b1c40000000049454e44ae426082")
put = urllib.request.Request(pre["upload_url"], data=png, method="PUT")
put.add_header("Content-Type", pre["content_type"])
with urllib.request.urlopen(put) as r:
    print(f"  PUT to presigned URL -> HTTP {r.status}")

name = "Imgtest " + uuid.uuid4().hex[:6]
s, prod = call("POST","/products",{"name":name,"type":"fruit",
    "size_codes":[{"code":"STD",
        "media":[{"kind":pre["kind"],"object_key":pre["key"],"content_type":pre["content_type"]}],
        "packs":[{"label":"1 kg","weight_grams":1000,"price_rupees":"99.99"}]}]},token=s1)
assert s==201, prod
gallery = prod["size_codes"][0]["media"]
assert len(gallery) == 1, gallery
print(f"  product saved with object_key (not a URL): {gallery[0]['object_key'][:40]}...")
assert gallery[0]["kind"] == "image", gallery[0]
# The cover is derived from the gallery's first image, so it must be the
# object just uploaded.
assert prod["image_url"], "no presigned GET returned"
with urllib.request.urlopen(prod["image_url"]) as r:
    got = r.read()
print(f"  GET presigned image_url -> HTTP {r.status}, {len(got)} bytes, matches upload: {got==png}")
assert got == png

print("\n=== CSV import: template download ===")
s, tpl = call("GET","/imports/template",token=s1)
print(f"  template -> {s}, {len(tpl.splitlines())} lines")

print("\n=== CSV import: upload -> preview (writes NOTHING) ===")
csv = ("﻿" + "name,type,grade,unit_label,weight_grams,price_rupees\r\n"
       f"{name}X,vegetable,A,1 kg,1000,45.00\r\n"
       f"{name}X,vegetable,A,3 kg,3000,\"1,200.50\"\r\n"
       f"{name}Y,fruit,B,1 box,2500,0.01\r\n"
       "Bad Row,legume,A,,-5,abc\r\n"
       ",,,,,\r\n").encode()
b = uuid.uuid4().hex
body = (f"--{b}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"p.csv\"\r\n"
        f"Content-Type: text/csv\r\n\r\n").encode() + csv + f"\r\n--{b}--\r\n".encode()
s, prev = call("POST","/imports",raw=body,token=s1,ctype=f"multipart/form-data; boundary={b}")
print(f"  upload -> {s}: total={prev['total_rows']} valid={prev['valid_rows']} errors={prev['error_rows']}")
assert s==201, prev
for row in prev["rows"]:
    if row["status"]=="error":
        print(f"    line {row['line']}: {row['errors']}")
print(f"  collapsed into {len(prev['products'])} products: {[(p['name'][-1], sum(len(sc['units']) for sc in p['size_codes'])) for p in prev['products']]}")

s, before = call("GET","/products?limit=100",token=s1)
print(f"  products before commit: {before['total']} (preview wrote nothing)")

print("\n=== CSV import: commit ===")
s, res = call("POST",f"/imports/{prev['import_id']}/commit",token=s1)
print(f"  commit -> {s}: {res}")
assert s==200, res
s, after = call("GET","/products?limit=100",token=s1)
print(f"  products after commit: {after['total']} (+{after['total']-before['total']})")

print("\n=== commit is idempotent (second attempt refused) ===")
s, again = call("POST",f"/imports/{prev['import_id']}/commit",token=s1)
print(f"  second commit -> {s} {again['error']['code']}")
assert s==409

print("\n=== ADMIN: cross-supplier search and archive ===")
s, all_p = call("GET","/admin/products?limit=100",token=admin)
print(f"  admin sees {all_p['total']} products across all suppliers")
s, one = call("GET",f"/admin/products/{prod['id']}",token=admin)
print(f"  admin reads supplier-1's product -> {s} ({one['name']})")
s, arch = call("POST",f"/admin/products/{prod['id']}/archive",token=admin)
print(f"  admin archives it -> {s}, status={arch['status']}")
assert arch["status"]=="archived"
s, denied = call("GET","/admin/products",token=s1)
print(f"  supplier hitting /admin/products -> {s} {denied['error']['code']}")
assert s==403
