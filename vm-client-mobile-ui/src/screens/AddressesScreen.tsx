/**
 * The address book.
 *
 * Addresses are soft-deleted server-side and orders snapshot them at placement
 * (CLAUDE.md §5.1), so removing one here never rewrites where a past order
 * went. The confirmation says so, because "remove" otherwise reads as though
 * it might.
 */
import { useCallback, type ReactElement } from "react";
import { Alert, Pressable, View } from "react-native";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Badge, EmptyState, ErrorState, Loading } from "../components/Feedback";
import { Screen } from "../components/Screen";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { api } from "../lib/client";
import type { Address } from "../lib/types";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import type { AccountStackParamList } from "../navigation/types";
import { colors, radius, spacing } from "../theme/tokens";

export const ADDRESSES_KEY = ["addresses"] as const;

/** The address as one block of text, used by every screen that shows one. */
export function addressLines(address: Address): string[] {
  return [
    [address.line1, address.line2, address.landmark].filter(Boolean).join(", "),
    `${address.city}, ${address.state} ${address.pincode}`,
    address.phone,
  ].filter((line) => line.trim() !== "" && line.trim() !== ",");
}

export function AddressesScreen(): ReactElement {
  const navigation = useNavigation<NativeStackNavigationProp<AccountStackParamList>>();
  const queryClient = useQueryClient();

  const addresses = useQuery<{ addresses: Address[] }, ApiError>({
    queryKey: ADDRESSES_KEY,
    queryFn: () => api.get<{ addresses: Address[] }>("/addresses"),
  });

  const invalidate = useCallback(
    () => void queryClient.invalidateQueries({ queryKey: ADDRESSES_KEY }),
    [queryClient],
  );

  const { refreshing, onRefresh } = usePullToRefresh(addresses.refetch);

  if (addresses.isPending) return <Loading label="Loading your addresses…" />;

  if (addresses.isError) {
    return (
      <Screen>
        <ErrorState error={addresses.error} onRetry={onRefresh} />
      </Screen>
    );
  }

  const list = addresses.data.addresses;

  return (
    <Screen
      onRefresh={onRefresh}
      refreshing={refreshing}
      footer={
        <Button
          label="Add an address"
          onPress={() => navigation.navigate("AddressForm")}
          block
        />
      }
    >
      {list.length === 0 ? (
        <EmptyState
          title="No saved addresses yet"
          body="Add one so checkout is a single tap."
        />
      ) : (
        list.map((address) => (
          <AddressCard
            key={address.id}
            address={address}
            onEdit={() => navigation.navigate("AddressForm", { addressId: address.id })}
            onChanged={invalidate}
          />
        ))
      )}
    </Screen>
  );
}

function AddressCard({
  address,
  onEdit,
  onChanged,
}: {
  address: Address;
  onEdit: () => void;
  onChanged: () => void;
}): ReactElement {
  const remove = useMutation<unknown, ApiError>({
    mutationFn: () => api.delete(`/addresses/${address.id}`),
    onSuccess: onChanged,
  });

  const makeDefault = useMutation<unknown, ApiError>({
    mutationFn: () => api.post(`/addresses/${address.id}/default`, {}),
    onSuccess: onChanged,
  });

  const confirmRemove = useCallback(() => {
    Alert.alert(
      "Remove this address?",
      "Orders already placed to it are unaffected — each order keeps its own copy of the address.",
      [
        { text: "Keep it", style: "cancel" },
        { text: "Remove", style: "destructive", onPress: () => remove.mutate() },
      ],
    );
  }, [remove]);

  const busy = remove.isPending || makeDefault.isPending;
  const error = remove.error ?? makeDefault.error;

  return (
    // A default address is outlined rather than badged alone: on a list of
    // three near-identical addresses the outline is what the eye finds first.
    <Card {...(address.is_default && { style: { borderColor: colors.primary[300] } })}>
      <View style={{ gap: spacing.md }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}>
          <Text variant="bodyStrong" tone="strong" style={{ flex: 1 }}>
            {address.label ?? "Address"}
          </Text>
          {address.is_default && <Badge label="Default" tone="primary" />}
        </View>

        <View>
          <Text tone="strong">{address.recipient_name}</Text>
          {addressLines(address).map((line) => (
            <Text key={line} variant="caption" tone="muted">
              {line}
            </Text>
          ))}
        </View>

        {error !== null && (
          <Text variant="caption" tone="danger" accessibilityRole="alert">
            {error.message}
          </Text>
        )}

        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.sm }}>
          {!address.is_default && (
            <Button
              label="Make default"
              variant="secondary"
              size="sm"
              disabled={busy}
              onPress={() => makeDefault.mutate()}
            />
          )}
          <Button label="Edit" variant="secondary" size="sm" disabled={busy} onPress={onEdit} />
          <Button
            label="Remove"
            variant="danger"
            size="sm"
            disabled={busy}
            onPress={confirmRemove}
          />
        </View>
      </View>
    </Card>
  );
}

/**
 * One selectable address, for checkout.
 *
 * Lives here rather than in the checkout screen so the address a customer
 * picks looks exactly like the address they saved — two renderings of the same
 * thing is how a "wrong address" support ticket starts.
 */
export function AddressChoice({
  address,
  selected,
  onSelect,
}: {
  address: Address;
  selected: boolean;
  onSelect: () => void;
}): ReactElement {
  const lines = addressLines(address);

  return (
    <Pressable
      onPress={onSelect}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      accessibilityLabel={`${address.label ?? "Address"}, ${address.recipient_name}, ${lines.join(", ")}`}
      style={{
        gap: 2,
        padding: spacing.md,
        borderRadius: radius.card,
        borderWidth: 1,
        borderColor: selected ? colors.primary[600] : colors.surface.border,
        backgroundColor: selected ? colors.primary[50] : colors.surface.raised,
      }}
    >
      <Text variant="bodyStrong" tone="strong">
        {address.label ?? "Address"} · {address.recipient_name}
      </Text>
      {lines.map((line) => (
        <Text key={line} variant="caption" tone="muted">
          {line}
        </Text>
      ))}
    </Pressable>
  );
}
