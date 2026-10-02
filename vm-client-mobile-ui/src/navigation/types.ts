/**
 * The navigation graph, typed.
 *
 * Declaring the param lists in one place is what makes `navigate("Product",
 * { productId })` a compile error when the shape is wrong, instead of a screen
 * that renders empty on a customer's phone.
 */
import type { NavigatorScreenParams } from "@react-navigation/native";

export type ShopStackParamList = {
  Catalog: undefined;
  Product: { productId: string; name: string };
};

export type CartStackParamList = {
  CartList: undefined;
  Checkout: undefined;
  /**
   * The same screen the Account tab uses, registered here too.
   *
   * A customer who reaches checkout without a usable address should not have
   * to leave the cart, find the Account tab and come back — that is where a
   * checkout gets abandoned. Pushing it inside THIS stack keeps the cart
   * underneath, so Back returns to the order they were placing.
   *
   * `addressId` absent means "add"; present means "edit".
   */
  AddressForm: { addressId?: string } | undefined;
};

export type OrdersStackParamList = {
  OrderList: undefined;
  OrderDetail: { orderId: string; orderNumber: string };
};

export type AccountStackParamList = {
  AccountHome: undefined;
  Addresses: undefined;
  /** The prepaid wallet and scheduled orders — CLAUDE.md §6.7. */
  Wallet: undefined;
  Schedules: undefined;
  ScheduleDetail: { scheduleId: string };
  /** `addressId` absent means "add"; present means "edit". */
  AddressForm: { addressId?: string } | undefined;
};

export type MainTabParamList = {
  Shop: NavigatorScreenParams<ShopStackParamList>;
  Cart: NavigatorScreenParams<CartStackParamList>;
  Orders: NavigatorScreenParams<OrdersStackParamList>;
  Account: NavigatorScreenParams<AccountStackParamList>;
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
