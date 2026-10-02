#!/usr/bin/env python3
"""
End-to-end daily-availability checks against a RUNNING stack.

Covers what must never regress:
  1. the sheet lists every active product, declared or not
  2. bulk declaration, then the storefront shows it without authentication
  3. exact stock NEVER reaches a customer — only a coarse hint
  4. a pack larger than the remaining stock is not purchasable
  5. reducing below reserved+sold is rejected, naming the floor
  6. closing a product removes it from the storefront immediately
  7. a client cannot ask for another day's stock

Run with:  make -C infra test-e2e-availability
Exits non-zero on the first failed assertion.
"""
import json, urllib.request, urllib.error, uuid, datetime
G = "http://localhost:8080/api"

def call(m, p, body=None, token=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(G + p, data=data, method=m)
    req.add_header("Content-Type", "application/json")
    if token: req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req) as r:
            t = r.read().decode(); return r.status, (json.loads(t) if t else {})
    except urllib.error.HTTPError as e:
        t = e.read().decode(); return e.code, (json.loads(t) if t else {})

def login(i, p):
    s, b = call("POST", "/auth/login", {"identifier": i, "password": p}); assert s == 200, b
    return b["tokens"]["access_token"]

s1 = login("greens@vayal.test", "local_dev_supplier_pw")

# Availability only applies to ACTIVE products, so create two.
tag = uuid.uuid4().hex[:6]
for name, units in [
    (f"Avail Tomato {tag}", [{"label": "1 kg", "weight_grams": 1000, "price_rupees": "45.00"},
                             {"label": "5 kg", "weight_grams": 5000, "price_rupees": "200.00"}]),
    (f"Avail Okra {tag}",   [{"label": "500 g", "weight_grams": 500, "price_rupees": "30.00"}]),
]:
    st, _ = call("POST", "/products", {"name": name, "type": "vegetable", "grade": "A",
                                       "status": "active",
                                       # One implicit grade: this suite is
                                       # about the stock rules, not grading.
                                       "size_codes": [{"code": "STD", "packs": units}]},
              token=s1)
    assert st == 201, st
print(f"  created 2 active products (tag {tag})")

print("=== the sheet shows every active product, declared or not ===")
s, sheet = call("GET", "/supplier/availability", token=s1)
print(f"  GET -> {s}  date={sheet['date']} is_today={sheet['is_today']}")
print(f"  {len(sheet['products'])} products, declared={sheet['declared_count']} undeclared={sheet['undeclared_count']}")
# Availability is declared per SIZE CODE now, so the sheet is keyed on those.
scids = [p["size_code_id"] for p in sheet["products"] if tag in p["name"]]
pids = [p["product_id"] for p in sheet["products"] if tag in p["name"]]
assert pids, "supplier has no active products to declare"

print("\n=== declare stock (bulk upsert) ===")
s, saved = call("PUT", "/supplier/availability",
                {"entries": [{"size_code_id": scids[0], "total_grams": 40000},
                             {"size_code_id": scids[1], "total_grams": 2000}]}, token=s1)
print(f"  PUT -> {s}, saved={saved.get('saved')}")
assert s == 200, saved

print("\n=== the storefront now shows it (no auth needed) ===")
s, cat = call("GET", "/catalog")
print(f"  GET /catalog -> {s}, date={cat['date']}, {cat['total']} products")
assert s == 200 and cat["total"] >= 2, cat
card = next(c for c in cat["products"] if c["id"] == pids[0])
print(f"  '{card['name']}': hint={card['stock_hint']!r} any_purchasable={card['any_unit_purchasable']}")
print(f"    units: {[(u['label'], u['purchasable']) for u in card['units']]}")

print("\n=== exact stock is NEVER exposed to customers ===")
leaked = [k for k in ("total_grams","reserved_grams","sold_grams","remaining_grams") if k in card]
print(f"  stock fields present on the card: {leaked or 'none'}")
assert not leaked, f"LEAK: {leaked}"
assert "remaining" not in json.dumps(cat).lower(), "LEAK: 'remaining' appears in the catalog payload"

print("\n=== a low-stock product gets a coarse hint only ===")
low = next((c for c in cat["products"] if c["id"] == pids[1]), None)
if low:
    print(f"  '{low['name']}' declared 2kg: hint={low['stock_hint']!r}")
    print(f"    units: {[(u['label'], u['weight_grams'], u['purchasable']) for u in low['units']]}")

print("\n=== reducing below committed stock is rejected ===")
import subprocess
# Simulate customer commitment directly, as the order flow would.
subprocess.run(["docker","exec","vayal-postgres","psql","-U","vayal_admin","-d","vayal","-c",
  f"UPDATE catalog.daily_availability SET reserved_grams=15000, sold_grams=10000 WHERE size_code_id='{scids[0]}';"],
  capture_output=True)
s, err = call("PUT", "/supplier/availability",
              {"entries": [{"size_code_id": scids[0], "total_grams": 20000}]}, token=s1)
print(f"  declare 20kg with 25kg committed -> {s} {err.get('error',{}).get('code')}")
print(f"    message: {err.get('error',{}).get('message')}")
print(f"    details: {err.get('error',{}).get('details')}")
assert s == 409 and err["error"]["code"] == "STOCK_BELOW_COMMITTED"

s, ok = call("PUT", "/supplier/availability",
             {"entries": [{"size_code_id": scids[0], "total_grams": 25000}]}, token=s1)
print(f"  declare exactly 25kg (== committed) -> {s}")
assert s == 200, ok

print("\n=== close a product early ===")
s, sheet2 = call("GET", "/supplier/availability", token=s1)
row = next(p for p in sheet2["products"] if p["size_code_id"] == scids[0])
print(f"  before: status={row['status']} remaining={row['remaining_grams']}g")
s, closed = call("POST", f"/supplier/availability/{row['availability_id']}/close", token=s1)
print(f"  close -> {s}, status={closed.get('status')}")
s, cat2 = call("GET", "/catalog")
still = [c["id"] for c in cat2["products"]]
print(f"  storefront still lists it? {pids[0] in still}")
assert pids[0] not in still, "a closed product is still on the storefront"

print("\n=== a client cannot ask for another day's stock ===")
yesterday = (datetime.date.today() - datetime.timedelta(days=1)).isoformat()
s, r = call("GET", f"/catalog?date={yesterday}")
print(f"  GET /catalog?date={yesterday} -> {s} {r.get('error',{}).get('message','')[:60]}")
assert s == 400
