/**
 * Naming a line by its GRADE.
 *
 * A supplier grades produce into separate crates at separate prices
 * (CLAUDE.md §5.2), so "Pomegranate · 1 Kg Box" is not a complete description
 * of anything: two lines of an order, a cart or a courier sheet can read
 * identically and be different goods.
 *
 * A copy of @vayal/ui-kit's `gradeLabel`/`gradedName`, because this app sits
 * outside the npm workspaces and cannot import the kit. `grade.test.ts` holds
 * the same cases the kit's rule is written to, so the two cannot drift without
 * the build saying so.
 */

/**
 * The implicit grade of an ungraded listing (vm-catalog-api migration 00009).
 * Never shown: a grower who does not grade has exactly one code.
 */
const DEFAULT_SIZE_CODE = "STD";

/** The grade as a chip, or "" when there is nothing worth showing. */
export function gradeLabel(sizeCode: string | null | undefined): string {
  const trimmed = (sizeCode ?? "").trim();
  if (trimmed === "" || trimmed.toUpperCase() === DEFAULT_SIZE_CODE) return "";
  return trimmed;
}

/**
 * The produce plus its grade: "Pomegranate (XL2)", or "Pomegranate" when the
 * listing is ungraded. For places with one string to work with — an
 * accessibility label, an alert. Where two elements fit, prefer the name with
 * `gradeLabel` beside it.
 */
export function gradedName(
  productName: string,
  sizeCode: string | null | undefined,
): string {
  const grade = gradeLabel(sizeCode);
  return grade === "" ? productName : `${productName} (${grade})`;
}
