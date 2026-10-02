/**
 * The navigation graph.
 *
 * Four tabs — Shop, Cart, Orders, Account — matching the web storefront's
 * header: today's produce, the cart button, the orders link and the account
 * menu. Shop is first and is what launches.
 *
 * The one structural difference from vm-supplier-mobile-ui, and it is
 * deliberate: this app does NOT swap the whole tree on sign-in. A shop that
 * demands an account before showing a price is a shop people close — the web
 * storefront lets anyone browse, and Apple's own review guidelines require an
 * app to work for the parts that do not need an account. So sign-in is a modal
 * pushed at the moment it is actually needed (adding to a cart, opening
 * Orders), and browsing never touches it.
 */
import type { ReactElement } from "react";
// Imported from its own entry point, not the `@expo/vector-icons` barrel. The
// barrel re-exports every icon set, and the bundler follows it: importing the
// package root ships all twelve font files — over 3 MB of TTFs, for one set.
import Ionicons from "@expo/vector-icons/Ionicons";
import { createBottomTabNavigator } from "@react-navigation/bottom-tabs";
import { createNativeStackNavigator } from "@react-navigation/native-stack";

import { Loading } from "../components/Feedback";
import { useAuth } from "../lib/auth";
import { useCart } from "../lib/cart";
import { AccountScreen } from "../screens/AccountScreen";
import { WalletScreen } from "../screens/WalletScreen";
import { ScheduleDetailScreen, SchedulesScreen } from "../screens/SchedulesScreen";
import { AddressFormScreen } from "../screens/AddressFormScreen";
import { AddressesScreen } from "../screens/AddressesScreen";
import { CartScreen } from "../screens/CartScreen";
import { CatalogScreen } from "../screens/CatalogScreen";
import { CheckoutScreen } from "../screens/CheckoutScreen";
import { LoginScreen } from "../screens/LoginScreen";
import { OrderDetailScreen } from "../screens/OrderDetailScreen";
import { OrdersScreen } from "../screens/OrdersScreen";
import { ProductScreen } from "../screens/ProductScreen";
import { colors, text } from "../theme/tokens";
import type {
  AccountStackParamList,
  CartStackParamList,
  MainTabParamList,
  OrdersStackParamList,
  RootStackParamList,
  ShopStackParamList,
} from "./types";

const RootStack = createNativeStackNavigator<RootStackParamList>();
const Tabs = createBottomTabNavigator<MainTabParamList>();
const ShopStack = createNativeStackNavigator<ShopStackParamList>();
const CartStack = createNativeStackNavigator<CartStackParamList>();
const OrdersStack = createNativeStackNavigator<OrdersStackParamList>();
const AccountStack = createNativeStackNavigator<AccountStackParamList>();

/** The header treatment every stack shares. */
const headerStyle = {
  headerStyle: { backgroundColor: colors.cream[50] },
  headerTintColor: colors.primary[700],
  headerTitleStyle: { color: text.strong, fontWeight: "600" as const },
  headerShadowVisible: false,
  contentStyle: { backgroundColor: colors.cream[100] },
};

function ShopNavigator(): ReactElement {
  return (
    <ShopStack.Navigator screenOptions={headerStyle}>
      <ShopStack.Screen
        name="Catalog"
        component={CatalogScreen}
        options={{ title: "Today’s produce" }}
      />
      <ShopStack.Screen
        name="Product"
        component={ProductScreen}
        // The produce's own name in the bar: a customer three taps deep should
        // not have to look at the photo to remember what they opened.
        options={({ route }) => ({ title: route.params.name })}
      />
    </ShopStack.Navigator>
  );
}

function CartNavigator(): ReactElement {
  return (
    <CartStack.Navigator screenOptions={headerStyle}>
      <CartStack.Screen
        name="CartList"
        component={CartScreen}
        options={{ title: "Your cart" }}
      />
      <CartStack.Screen
        name="Checkout"
        component={CheckoutScreen}
        options={{ title: "Checkout" }}
      />
      {/* Registered in both stacks on purpose — see CartStackParamList. The
          screen keeps its own state either way; only the stack it is pushed
          onto, and therefore what Back returns to, differs. */}
      <CartStack.Screen name="AddressForm" component={AddressFormScreen} />
    </CartStack.Navigator>
  );
}

function OrdersNavigator(): ReactElement {
  return (
    <OrdersStack.Navigator screenOptions={headerStyle}>
      <OrdersStack.Screen
        name="OrderList"
        component={OrdersScreen}
        options={{ title: "Your orders" }}
      />
      <OrdersStack.Screen
        name="OrderDetail"
        component={OrderDetailScreen}
        options={({ route }) => ({ title: route.params.orderNumber })}
      />
    </OrdersStack.Navigator>
  );
}

function AccountNavigator(): ReactElement {
  return (
    <AccountStack.Navigator screenOptions={headerStyle}>
      <AccountStack.Screen
        name="AccountHome"
        component={AccountScreen}
        options={{ title: "Account" }}
      />
      <AccountStack.Screen name="Wallet" component={WalletScreen} options={{ title: "Wallet" }} />
      <AccountStack.Screen
        name="Schedules"
        component={SchedulesScreen}
        options={{ title: "Scheduled orders" }}
      />
      <AccountStack.Screen
        name="ScheduleDetail"
        component={ScheduleDetailScreen}
        options={{ title: "Schedule" }}
      />
      <AccountStack.Screen
        name="Addresses"
        component={AddressesScreen}
        options={{ title: "Delivery addresses" }}
      />
      <AccountStack.Screen
        name="AddressForm"
        component={AddressFormScreen}
        // Set by the screen once it knows whether this is an add or an edit.
        options={{ title: "Address" }}
      />
    </AccountStack.Navigator>
  );
}

type IoniconName = keyof typeof Ionicons.glyphMap;

const TAB_ICONS: Record<keyof MainTabParamList, [active: IoniconName, inactive: IoniconName]> = {
  Shop: ["leaf", "leaf-outline"],
  Cart: ["cart", "cart-outline"],
  Orders: ["receipt", "receipt-outline"],
  Account: ["person-circle", "person-circle-outline"],
};

function MainTabs(): ReactElement {
  const { count } = useCart();

  return (
    <Tabs.Navigator
      screenOptions={({ route }) => ({
        ...headerStyle,
        headerShown: false,
        tabBarActiveTintColor: colors.primary[600],
        tabBarInactiveTintColor: text.muted,
        tabBarStyle: {
          backgroundColor: colors.cream[50],
          borderTopColor: colors.cream[300],
        },
        tabBarIcon: ({ focused, color, size }) => {
          const [active, inactive] = TAB_ICONS[route.name];
          return <Ionicons name={focused ? active : inactive} size={size} color={color} />;
        },
      })}
    >
      <Tabs.Screen name="Shop" component={ShopNavigator} options={{ title: "Shop" }} />
      <Tabs.Screen
        name="Cart"
        component={CartNavigator}
        options={{
          title: "Cart",
          // The badge is the whole reason the cart is a tab rather than a
          // button: it is visible from every screen without opening anything.
          // Undefined, not 0 — a badge reading "0" is worse than no badge.
          ...(count > 0 && {
            tabBarBadge: count,
            tabBarBadgeStyle: { backgroundColor: colors.accent[500] },
            tabBarAccessibilityLabel: `Cart, ${count} item${count === 1 ? "" : "s"}`,
          }),
        }}
      />
      <Tabs.Screen name="Orders" component={OrdersNavigator} options={{ title: "Orders" }} />
      <Tabs.Screen name="Account" component={AccountNavigator} options={{ title: "Account" }} />
    </Tabs.Navigator>
  );
}

export function RootNavigator(): ReactElement {
  const { initialising } = useAuth();

  // Waits for the stored refresh token to be tried. Mounting the tabs first
  // would fire the cart query as a signed-out user and get a 401 for a session
  // that is one await away from existing.
  if (initialising) return <Loading label="Getting things ready…" />;

  return (
    <RootStack.Navigator>
      <RootStack.Screen name="Main" component={MainTabs} options={{ headerShown: false }} />
      <RootStack.Screen
        name="Login"
        component={LoginScreen}
        options={{
          // A modal, not a route the tabs can strand you on: dismissing it
          // returns to whatever the customer was doing — usually a full
          // catalogue and an item they were about to add.
          presentation: "modal",
          title: "Sign in",
          ...headerStyle,
        }}
      />
    </RootStack.Navigator>
  );
}
