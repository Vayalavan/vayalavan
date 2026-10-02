# Analytics metrics

What the analytics schema can answer, and where each figure comes from. Every
source below is a table in schema `analytics`, filled by the CDC pipeline
([analytics.md](analytics.md)). Money is integer paise; format it at the UI.

**Conventions used throughout**

- **Paid** means `orders.status IN ('paid', 'processed', 'dispatched')`, the
  same set the admin dashboard counts as revenue.
- **Period** is `orders.placed_date_ist`, already the IST business day, so no
  query needs a timezone.
- `v_order_lines` is `order_items` joined to its order and to the supplier's
  name. Use it for anything per product, grade or supplier.

---

## Admin

| Metric | Where it comes from |
|---|---|
| Orders placed | `count(*)` of `orders`, by `placed_date_ist` |
| Paid orders | `orders` where status is paid |
| GMV | `sum(orders.total_paise)` of paid orders |
| Average order value | GMV ÷ paid orders |
| Checkout conversion | paid orders ÷ orders placed |
| Abandoned checkouts | `orders.status = 'expired'`, `expired_at` |
| Cancellations, and refunds owed | `orders.status = 'cancelled'`, `cancelled_at`. A refund is owed when `cancelled_from_status` is `paid` or `processed` |
| Platform fee revenue | `sum(orders.platform_fee_paise)`, paid orders |
| Delivery fee revenue | `sum(orders.delivery_fee_paise)`, paid orders |
| Markup revenue | `sum(orders.markup_paise)`, paid orders |
| Margin on goods (markup + commission) | `sum(orders.subtotal_paise − orders.supplier_payable_paise)`, paid orders |
| Owed to suppliers | `sum(orders.supplier_payable_paise)`, paid orders |
| Time to pay | `orders.paid_at − orders.placed_at` |
| Time to process / dispatch | `processed_at − paid_at`, `dispatched_at − processed_at` |
| Dispatched on time | `orders.dispatched_at` against `orders.delivery_day` |
| Before vs after the 4pm cutoff | `orders.placed_before_cutoff` |
| Orders by hour of day | `orders.placed_hour_ist` |
| Checkout vs scheduled orders | `orders.source` (`checkout` / `schedule`), `schedule_id` |
| Payment mix | `orders.payment_method`, `orders.paid_via` (webhook / admin manual / wallet) |
| Customers, new vs returning | `orders.customer_id`; returning = an earlier paid `placed_at` for the same id |
| Basket size | `orders.item_count`, `total_qty`, `total_grams` |
| Sales by city / state / pincode | `orders.ship_city`, `ship_state`, `ship_pincode` |
| Sales by product, category, grade, pack | `v_order_lines.product_name`, `product_type`, `size_code`, `pack_label` |
| Top suppliers by sales | `v_order_lines.supplier_name`, `sum(line_total_paise)` |
| Catalogue size (live / draft / archived) | `products.status` |
| New listings, and how they arrived | `products.created_at`, `first_active_at`, `source` (form / csv_import) |
| Price changes | `product_packs` versions (`valid_from`, `valid_to`) per `pack_option_id` |
| Price range on sale | `products.min_price_paise`, `max_price_paise` |
| Listings without photos | `products.image_count = 0` |
| Supplier onboarding | `suppliers.status`, `created_at` → `approved_at` |
| Suppliers by town | `suppliers.city`, `state` |
| Pipeline freshness | `max(last_event_at)` over `orders`, `products`, `suppliers` |

## Supplier

Every row is filtered to one `supplier_id`. That is the id the gateway sends
as `X-Supplier-Id`, so a supplier can only ever see their own figures.

| Metric | Where it comes from |
|---|---|
| Gross sales | `sum(v_order_lines.line_total_paise)`, paid orders |
| Sales before our markup | `sum(line_total_paise − line_markup_paise)`, paid orders |
| Orders containing their goods | `count(DISTINCT v_order_lines.order_id)`, paid orders |
| Units sold | `sum(v_order_lines.qty)` |
| Kilograms sold | `sum(v_order_lines.line_grams) ÷ 1000` |
| Average price per kg | `sum(line_total_paise) ÷ sum(line_grams)` |
| Daily / weekly trend | `v_order_lines` grouped by `placed_date_ist` |
| Best sellers | `v_order_lines` grouped by `product_name` |
| Grade mix | `v_order_lines` grouped by `size_code` |
| Pack-size mix | `v_order_lines` grouped by `pack_label`, `weight_grams` |
| Customers reached, and repeat buyers | `count(DISTINCT customer_id)`; repeat = more than one paid order |
| Where their produce goes | `v_order_lines.ship_city`, `ship_state` |
| Lost sales | `v_order_lines` where status is `expired` or `cancelled` |
| Share of orders before the cutoff | `v_order_lines.placed_before_cutoff` |
| Catalogue (live / draft / archived) | `products` where `supplier_id` matches, by `status` |
| Packs on sale and price range | `products.active_pack_count`, `min_price_paise`, `max_price_paise` |
| Their price history | `product_packs` for their `product_id`s |
| Listings without photos or video | `products.image_count`, `video_count` |
| Account status, approval date, commission | `suppliers.status`, `approved_at`, `commission_bps` |

---

## Not answerable yet

These need data the pipeline doesn't capture. Each needs one small addition.

| Wanted | Missing | Addition needed |
|---|---|---|
| Supplier's payout per order, after commission | The order event carries per-supplier `payouts[]`, but the consumer keeps only the order total (`orders.supplier_payable_paise`) | An `analytics.order_payouts` table in the consumer, filled from the same events. No new outbox. |
| Commission when a supplier is on the platform default | `suppliers.commission_bps` is NULL for "default", and the default rate isn't in analytics | Carry the effective rate in the supplier event, or read it from config in vm-analytics-api |
| Stock declared vs sold (sell-through), sold-out time | Daily availability isn't captured | An availability event from catalog (declared / closed / committed, per grade per day) |
| Settlement: paid vs outstanding, days to pay a supplier | Marking a payout paid doesn't emit an event | A `payout.paid` event in `orders.order_events_outbox` |
| Refunds actually issued | No code sets `refunded` yet; wallet refunds aren't captured | A refund event once refunds are wired |
