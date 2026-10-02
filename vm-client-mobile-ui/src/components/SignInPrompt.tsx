/**
 * What the Cart, Orders and Account tabs show to someone who is not signed in.
 *
 * A card with a reason and a button, not a redirect. Bouncing a tab straight
 * into a sign-in modal would make three of the four tabs untappable without an
 * account, which is both hostile and the thing App Store review section 5.1.1
 * objects to. Browsing works signed out; these three cannot, and this says why.
 */
import { useCallback, type ReactElement } from "react";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";

import type { RootStackParamList } from "../navigation/types";
import { Button } from "./Button";
import { Screen } from "./Screen";
import { EmptyState } from "./Feedback";

export function SignInPrompt({
  title,
  body,
}: {
  title: string;
  body: string;
}): ReactElement {
  // The modal lives on the ROOT stack, above the tabs, so it is reached
  // through the parent navigator rather than the tab's own stack.
  const navigation = useNavigation<NativeStackNavigationProp<RootStackParamList>>();

  const openSignIn = useCallback(() => navigation.navigate("Login"), [navigation]);

  return (
    <Screen>
      <EmptyState
        title={title}
        body={body}
        action={<Button label="Sign in or create an account" onPress={openSignIn} block />}
      />
    </Screen>
  );
}
