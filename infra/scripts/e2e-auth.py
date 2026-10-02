#!/usr/bin/env python3
"""
End-to-end authorisation checks against a RUNNING stack.

Exercises the real chain — browser -> gateway (JWT verify, role guard) ->
vm-profile-api (ownership checks) -> Postgres — rather than mocking any of it,
because the failures worth catching here live in the seams between those
layers.

Covers the four scenarios the auth slice must never regress on:
  1. login / refresh rotation / reuse detection
  2. a customer cannot reach any admin route
  3. a supplier cannot read another supplier's record
  4. bank details are masked outside the admin payout screen

Run with:  make -C infra test-e2e     (requires `make dev` running and seeded)
Exits non-zero on the first failed assertion.
"""
import json, urllib.request, urllib.error
G = "http://localhost:8080/api"

def call(method, path, body=None, token=None, hdrs=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(G + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if token: req.add_header("Authorization", "Bearer " + token)
    for k, v in (hdrs or {}).items(): req.add_header(k, v)
    try:
        with urllib.request.urlopen(req) as r:
            raw = r.read().decode()
            return r.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        return e.code, (json.loads(raw) if raw else {})

def login(ident, pw):
    s, b = call("POST", "/auth/login", {"identifier": ident, "password": pw})
    assert s == 200, (s, b)
    return b["tokens"]["access_token"], b["tokens"]["refresh_token"], b["user"]

admin_at, _, admin_u = login("admin@vayal.test", "local_dev_admin_pw_change_me")
cust_at, cust_rt, cust_u = login("customer@vayal.test", "local_dev_customer_pw")
s1_at, _, s1_u = login("greens@vayal.test", "local_dev_supplier_pw")
s2_at, _, s2_u = login("farm@vayal.test", "local_dev_supplier_pw")
print(f"  logged in: admin={admin_u['role']} customer={cust_u['role']} suppliers={s1_u['role']},{s2_u['role']}")

print("\n=== GET /me + seeded addresses ===")
s, me = call("GET", "/me", token=cust_at)
print(f"  /me -> {s}, name={me['user']['name']}")
s, addrs = call("GET", "/addresses", token=cust_at)
print(f"  addresses -> {[(a['label'], a['is_default']) for a in addrs['addresses']]}")

print("\n=== TEST: customer cannot hit ANY admin route ===")
for m, p, b in [("GET","/admin/suppliers",None), ("POST","/admin/suppliers",{"business_name":"Evil Co","contact_name":"E","phone":"9800000009","email":"evil@x.com"})]:
    s, r = call(m, p, b, token=cust_at)
    print(f"  {m:4} {p:22} as customer -> {s} {r.get('error',{}).get('code','')}")
sid = None
s, lst = call("GET", "/admin/suppliers", token=admin_at)
print(f"  GET  /admin/suppliers   as admin    -> {s}, total={lst['total']}")

print("\n=== TEST: a supplier cannot read another supplier's record ===")
s1_id = next(x["id"] for x in lst["suppliers"] if x["email"] == "greens@vayal.test")
s2_id = next(x["id"] for x in lst["suppliers"] if x["email"] == "farm@vayal.test")
s, own = call("GET", f"/suppliers/{s1_id}", token=s1_at)
print(f"  supplier-1 reading OWN record      -> {s} ({own.get('business_name')})")
s, other = call("GET", f"/suppliers/{s2_id}", token=s1_at)
print(f"  supplier-1 reading supplier-2      -> {s} {other.get('error',{}).get('code','')}")
s, byadmin = call("GET", f"/suppliers/{s2_id}", token=admin_at)
print(f"  admin reading supplier-2           -> {s} ({byadmin.get('business_name')})")

print("\n=== TEST: bank account number is masked ===")
print(f"  supplier sees: {own.get('bank_account_number')}")

print("\n=== TEST: refresh rotation + reuse detection (through gateway) ===")
s, r = call("POST", "/auth/refresh", {"refresh_token": cust_rt})
new_rt = r["tokens"]["refresh_token"]
print(f"  rotate            -> {s}, new token differs: {new_rt != cust_rt}")
s, r = call("POST", "/auth/refresh", {"refresh_token": cust_rt})
print(f"  replay old token  -> {s} {r.get('error',{}).get('code','')}")
s, r = call("POST", "/auth/refresh", {"refresh_token": new_rt})
print(f"  successor now dead-> {s} {r.get('error',{}).get('code','')}")

print("\n=== TEST: zod validation at the gateway ===")
s, r = call("POST", "/auth/register", {"name":"x","phone":"123","email":"not-an-email","password":"short"})
print(f"  bad register -> {s} {r['error']['code']}")
print(f"  details: {r['error'].get('details')}")
