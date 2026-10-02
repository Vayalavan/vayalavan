/**
 * Add or edit a delivery address.
 *
 * A whole screen rather than the web's inline form: nine fields inside a card
 * inside a scroll view, on a phone, with a keyboard covering half of it, is
 * how a form gets abandoned. Its own screen means the keyboard has somewhere
 * to push the content to.
 *
 * Field-level errors come from the gateway's zod layer via `details`, and are
 * rendered against the box that caused them — a banner saying "check the
 * highlighted fields" with nothing highlighted is the version of this screen
 * that wastes someone's afternoon.
 */
import { useCallback, useEffect, useState, type ReactElement } from "react";
import { View } from "react-native";
import type { NativeStackScreenProps } from "@react-navigation/native-stack";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Banner, Loading } from "../components/Feedback";
import { TextField } from "../components/Field";
import { Screen } from "../components/Screen";
import type { ApiError } from "../lib/api";
import { api } from "../lib/client";
import { DELIVERY_AREA_LABEL, PINCODE_AREA_ERROR, isServiceablePincode } from "../lib/pincode";
import type { Address, AddressDraft } from "../lib/types";
import type { AccountStackParamList } from "../navigation/types";
import { spacing } from "../theme/tokens";
import { ADDRESSES_KEY } from "./AddressesScreen";

type Props = NativeStackScreenProps<AccountStackParamList, "AddressForm">;

const EMPTY: AddressDraft = {
  label: "Home",
  recipient_name: "",
  phone: "",
  line1: "",
  line2: "",
  landmark: "",
  city: "",
  state: "",
  pincode: "",
};

type FieldErrors = Partial<Record<keyof AddressDraft, string>>;

export function AddressFormScreen({ navigation, route }: Props): ReactElement {
  const addressId = route.params?.addressId;
  const editing = addressId !== undefined;
  const queryClient = useQueryClient();

  const [fields, setFields] = useState<AddressDraft>(EMPTY);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [banner, setBanner] = useState<string | null>(null);
  // Seeded once from the server's copy; afterwards the form owns the values,
  // or a background refetch would overwrite what someone is typing.
  const [seeded, setSeeded] = useState(!editing);

  useEffect(() => {
    navigation.setOptions({ title: editing ? "Edit address" : "New address" });
  }, [editing, navigation]);

  // Read from the list already in the cache where possible: arriving here from
  // the address book should not show a spinner for something on the previous
  // screen. The query is the fallback for a cold cache (a deep link, or a
  // resume after the cache was collected).
  const existing = useQuery<{ addresses: Address[] }, ApiError>({
    queryKey: ADDRESSES_KEY,
    queryFn: () => api.get<{ addresses: Address[] }>("/addresses"),
    enabled: editing,
  });

  useEffect(() => {
    if (seeded || !editing || !existing.data) return;
    const found = existing.data.addresses.find((address) => address.id === addressId);
    if (!found) return;
    setFields({
      label: found.label ?? "",
      recipient_name: found.recipient_name,
      phone: found.phone,
      line1: found.line1,
      line2: found.line2 ?? "",
      landmark: found.landmark ?? "",
      city: found.city,
      state: found.state,
      pincode: found.pincode,
    });
    setSeeded(true);
  }, [addressId, editing, existing.data, seeded]);

  const save = useMutation<unknown, ApiError>({
    mutationFn: () =>
      editing
        ? api.put(`/addresses/${addressId}`, fields)
        : api.post("/addresses", fields),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ADDRESSES_KEY });
      navigation.goBack();
    },
    onError: (error) => {
      const details = error.details as FieldErrors | undefined;
      setErrors(details ?? {});
      if (!details || Object.keys(details).length === 0) setBanner(error.message);
    },
  });

  const set = useCallback((key: keyof AddressDraft, value: string) => {
    setFields((current) => ({ ...current, [key]: value }));
  }, []);

  /**
   * The delivery area, checked at the field so the customer is not told after
   * a round trip. vm-profile-api is what actually enforces it, and
   * vm-orders-api checks again at checkout.
   *
   * Held back until six digits are typed: complaining about the area while
   * someone is still typing the PIN code is complaining about a prefix.
   */
  const pincodeAreaError =
    fields.pincode.trim().length === 6 && !isServiceablePincode(fields.pincode)
      ? PINCODE_AREA_ERROR
      : undefined;

  const submit = useCallback(() => {
    setBanner(null);
    setErrors({});
    save.mutate();
  }, [save]);

  if (editing && !seeded && existing.isPending) return <Loading />;

  return (
    <Screen
      footer={
        <Button
          label={save.isPending ? "Saving…" : editing ? "Save changes" : "Add address"}
          onPress={submit}
          loading={save.isPending}
          disabled={
            fields.recipient_name.trim() === "" ||
            fields.phone.trim() === "" ||
            fields.line1.trim() === "" ||
            fields.city.trim() === "" ||
            fields.state.trim() === "" ||
            fields.pincode.trim() === "" ||
            // Saving would only fetch the same answer from the server, and on
            // a phone that is a spinner and a round trip for a known no.
            pincodeAreaError !== undefined
          }
          block
        />
      }
    >
      {banner !== null && <Banner tone="error" message={banner} />}

      <View style={{ gap: spacing.lg }}>
        <TextField
          label="Label"
          value={fields.label}
          onChangeText={(value) => set("label", value)}
          placeholder="Home, Office…"
          autoCapitalize="words"
          optional
          error={errors.label}
        />
        <TextField
          label="Recipient name"
          value={fields.recipient_name}
          onChangeText={(value) => set("recipient_name", value)}
          autoCapitalize="words"
          error={errors.recipient_name}
        />
        <TextField
          label="Phone"
          value={fields.phone}
          onChangeText={(value) => set("phone", value)}
          keyboardType="phone-pad"
          autoComplete="tel"
          autoCapitalize="none"
          hint="Whoever the courier should call on the day."
          error={errors.phone}
        />
        <TextField
          label="Address line 1"
          value={fields.line1}
          onChangeText={(value) => set("line1", value)}
          error={errors.line1}
        />
        <TextField
          label="Address line 2"
          value={fields.line2}
          onChangeText={(value) => set("line2", value)}
          optional
          error={errors.line2}
        />
        <TextField
          label="Landmark"
          value={fields.landmark}
          onChangeText={(value) => set("landmark", value)}
          optional
          error={errors.landmark}
        />
        <TextField
          label="City"
          value={fields.city}
          onChangeText={(value) => set("city", value)}
          autoCapitalize="words"
          error={errors.city}
        />
        <TextField
          label="State"
          value={fields.state}
          onChangeText={(value) => set("state", value)}
          autoCapitalize="words"
          error={errors.state}
        />
        <TextField
          label="PIN code"
          value={fields.pincode}
          onChangeText={(value) => set("pincode", value)}
          keyboardType="number-pad"
          autoCapitalize="none"
          hint={`We deliver in ${DELIVERY_AREA_LABEL}.`}
          error={pincodeAreaError ?? errors.pincode}
        />
      </View>
    </Screen>
  );
}
