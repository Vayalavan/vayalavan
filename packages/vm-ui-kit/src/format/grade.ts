/**
 * Naming a line by its GRADE, in one place.
 *
 * A supplier grades produce into separate crates at separate prices
 * (CLAUDE.md §5.2), so "Pomegranate · 1 Kg Box" is not a complete description
 * of anything: two lines of an order, a cart or a courier sheet can read
 * identically and be different goods. Every surface that names a line has to
 * say which grade, and every surface has to hide the same two cases — which is
 * why the rule lives here rather than as eight copies of a ternary.
 *
 * The Go services apply the same rule in `gradedName` (vm-orders-api), and the
 * two mobile apps carry their own copy with a test asserting it agrees, since
 * they sit outside the npm workspaces and cannot import this package.
 */

/**
 * The implicit grade of an ungraded listing (vm-catalog-api migration 00009).
 *
 * Never shown to a customer: a grower who does not grade has exactly one code,
 * and printing it offers a choice of one.
 */
const DEFAULT_SIZE_CODE = "STD";

/**
 * The grade as a chip, or "" when there is nothing worth showing.
 *
 * Null, empty, whitespace and the implicit default all come back empty, so a
 * caller can write `{gradeLabel(x) && <Chip …/>}` without repeating the rule.
 */
export function gradeLabel(sizeCode: string | null | undefined): string {
  const trimmed = (sizeCode ?? "").trim();
  if (trimmed === "" || trimmed.toUpperCase() === DEFAULT_SIZE_CODE) return "";
  return trimmed;
}

/**
 * The produce plus its grade: "Pomegranate (XL2)", or "Pomegranate" when the
 * listing is ungraded.
 *
 * For places that have one string to work with — an alert, an aria-label, a
 * spreadsheet cell. Where there is room for two elements, prefer the name with
 * `gradeLabel` beside it as a chip.
 */
export function gradedName(
  productName: string,
  sizeCode: string | null | undefined,
): string {
  const grade = gradeLabel(sizeCode);
  return grade === "" ? productName : `${productName} (${grade})`;
}
