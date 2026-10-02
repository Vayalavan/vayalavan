/**
 * The navigation graph, typed.
 *
 * Declaring the param lists in one place is what makes `navigate("ProductForm",
 * { productId })` a compile error when the shape is wrong, instead of a screen
 * that renders empty on a supplier's phone.
 */
import type { NavigatorScreenParams } from "@react-navigation/native";

export type ProductsStackParamList = {
  ProductList: undefined;
  /** `productId` absent means "create"; present means "edit". */
  ProductForm: { productId?: string } | undefined;
  Import: undefined;
};

export type MainTabParamList = {
  Today: undefined;
  Products: NavigatorScreenParams<ProductsStackParamList>;
  Sales: undefined;
  Insights: undefined;
  Account: undefined;
};

export type RootStackParamList = {
  Login: undefined;
  Main: NavigatorScreenParams<MainTabParamList>;
};

declare global {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace ReactNavigation {
    interface RootParamList extends RootStackParamList {}
  }
}
