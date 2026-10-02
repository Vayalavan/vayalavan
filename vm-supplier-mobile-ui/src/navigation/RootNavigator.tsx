/**
 * The navigation graph.
 *
 * The web app's nav links become tabs, in the same order and with the same
 * landing screen: today's availability is what a supplier opens this for at
 * 6am, so it is the first tab and the one that shows on launch.
 *
 * "Import CSV" is not a tab. It is a once-a-season job, and spending a slot on
 * a phone's bottom bar on it would shrink the things done daily. It lives in
 * the Products stack, reachable from the Products screen — which is where
 * someone thinking about their catalogue already is.
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
import { AccountScreen } from "../screens/AccountScreen";
import { AnalyticsScreen } from "../screens/AnalyticsScreen";
import { AvailabilityScreen } from "../screens/AvailabilityScreen";
import { ImportScreen } from "../screens/ImportScreen";
import { LoginScreen } from "../screens/LoginScreen";
import { ProductFormScreen } from "../screens/ProductFormScreen";
import { ProductsScreen } from "../screens/ProductsScreen";
import { SalesScreen } from "../screens/SalesScreen";
import { colors, text } from "../theme/tokens";
import type {
  MainTabParamList,
  ProductsStackParamList,
  RootStackParamList,
} from "./types";

const RootStack = createNativeStackNavigator<RootStackParamList>();
const Tabs = createBottomTabNavigator<MainTabParamList>();
const ProductsStack = createNativeStackNavigator<ProductsStackParamList>();

/** The header treatment every stack shares. */
const headerStyle = {
  headerStyle: { backgroundColor: colors.surface.raised },
  headerTintColor: colors.primary[700],
  headerTitleStyle: { color: text.strong, fontWeight: "600" as const },
  headerShadowVisible: false,
  contentStyle: { backgroundColor: colors.surface.DEFAULT },
};

function ProductsNavigator(): ReactElement {
  return (
    <ProductsStack.Navigator screenOptions={headerStyle}>
      <ProductsStack.Screen
        name="ProductList"
        component={ProductsScreen}
        options={{ title: "Your produce" }}
      />
      <ProductsStack.Screen
        name="ProductForm"
        component={ProductFormScreen}
        // The title depends on whether this is a create or an edit, so the
        // screen sets it itself once it knows.
        options={{ title: "Product" }}
      />
      <ProductsStack.Screen
        name="Import"
        component={ImportScreen}
        options={{ title: "Import CSV" }}
      />
    </ProductsStack.Navigator>
  );
}

type IoniconName = keyof typeof Ionicons.glyphMap;

const TAB_ICONS: Record<keyof MainTabParamList, [active: IoniconName, inactive: IoniconName]> = {
  Today: ["today", "today-outline"],
  Products: ["leaf", "leaf-outline"],
  Sales: ["cash", "cash-outline"],
  Insights: ["stats-chart", "stats-chart-outline"],
  Account: ["person-circle", "person-circle-outline"],
};

function MainTabs(): ReactElement {
  return (
    <Tabs.Navigator
      screenOptions={({ route }) => ({
        ...headerStyle,
        tabBarActiveTintColor: colors.primary[600],
        tabBarInactiveTintColor: text.muted,
        tabBarStyle: {
          backgroundColor: colors.surface.raised,
          borderTopColor: colors.surface.border,
        },
        tabBarIcon: ({ focused, color, size }) => {
          const [active, inactive] = TAB_ICONS[route.name];
          return <Ionicons name={focused ? active : inactive} size={size} color={color} />;
        },
      })}
    >
      <Tabs.Screen
        name="Today"
        component={AvailabilityScreen}
        options={{ title: "Today" }}
      />
      <Tabs.Screen
        name="Products"
        component={ProductsNavigator}
        // The stack inside draws its own headers; a second one above them
        // would stack two title bars.
        options={{ title: "Produce", headerShown: false }}
      />
      <Tabs.Screen name="Sales" component={SalesScreen} options={{ title: "Sales" }} />
      {/* After Sales, for the same reason as on the web: settlement is what a
          grower opens the app to check, insights are what they stay for. */}
      <Tabs.Screen name="Insights" component={AnalyticsScreen} options={{ title: "Insights" }} />
      <Tabs.Screen name="Account" component={AccountScreen} options={{ title: "Account" }} />
    </Tabs.Navigator>
  );
}

/**
 * Signed out or signed in — never both, and never a redirect between them.
 *
 * Swapping the whole tree rather than guarding each screen means there is no
 * moment where a protected screen is mounted without a session, and no history
 * entry that could take a signed-out supplier back into the app.
 */
export function RootNavigator(): ReactElement {
  const { user, initialising } = useAuth();

  // Waits for the stored refresh token to be tried. Rendering Login while that
  // is still in flight would flash a sign-in screen at an already signed-in
  // supplier on every cold start.
  if (initialising) return <Loading label="Signing you in…" />;

  return (
    <RootStack.Navigator screenOptions={{ headerShown: false }}>
      {user === null ? (
        <RootStack.Screen name="Login" component={LoginScreen} />
      ) : (
        <RootStack.Screen name="Main" component={MainTabs} />
      )}
    </RootStack.Navigator>
  );
}
