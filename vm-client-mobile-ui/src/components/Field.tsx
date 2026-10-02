/**
 * Form inputs: a labelled text field, and a chip-style choice row that stands
 * in for the web app's <select>.
 *
 * React Native has no native picker worth using on both platforms without a
 * dependency, and for these lists — four product types, three statuses — a row
 * of chips is fewer taps than any picker anyway, and shows every option at
 * once.
 */
import type { ReactElement, ReactNode, RefObject } from "react";
import {
  Pressable,
  TextInput,
  View,
  type KeyboardTypeOptions,
  type StyleProp,
  type TextStyle,
  type ViewStyle,
} from "react-native";

import { HIT_SIZE, colors, radius, spacing, text as palette } from "../theme/tokens";
import { Text } from "./Text";

export interface FieldProps {
  label: string;
  /** Rendered under the label, before the control. */
  hint?: string | undefined;
  /** Rendered under the control, in the danger tone, with role=alert. */
  error?: string | undefined;
  optional?: boolean;
  children: ReactNode;
}

/** Label, optional hint, control, error — the layout every field shares. */
export function Field({ label, hint, error, optional, children }: FieldProps): ReactElement {
  return (
    <View style={{ gap: spacing.xs }}>
      <Text variant="label" tone="strong">
        {label}
        {optional && (
          <Text variant="label" tone="faint">
            {"  (optional)"}
          </Text>
        )}
      </Text>
      {hint !== undefined && (
        <Text variant="caption" tone="muted">
          {hint}
        </Text>
      )}
      {children}
      {error !== undefined && error !== "" && (
        <Text variant="caption" tone="danger" accessibilityRole="alert">
          {error}
        </Text>
      )}
    </View>
  );
}

export interface TextFieldProps {
  label: string;
  /**
   * What a screen reader announces, when the visible label is too short to
   * stand alone. A list of number boxes each labelled "Available today" is
   * unnavigable without sight of the row it belongs to.
   */
  accessibilityLabel?: string;
  value: string;
  onChangeText: (value: string) => void;
  placeholder?: string;
  hint?: string | undefined;
  error?: string | undefined;
  optional?: boolean;
  secureTextEntry?: boolean;
  autoCapitalize?: "none" | "sentences" | "words" | "characters";
  autoComplete?: "username" | "email" | "tel" | "current-password" | "off";
  keyboardType?: KeyboardTypeOptions;
  multiline?: boolean;
  editable?: boolean;
  /** Right-aligns the value. For quantities, which read better against the unit. */
  alignRight?: boolean;
  /** Trailing text inside the box — "kg", "₹". */
  suffix?: string;
  onSubmitEditing?: () => void;
  returnKeyType?: "done" | "next" | "go";
  /**
   * A handle on the underlying TextInput, so one field can move focus to the
   * next. Named rather than a forwarded `ref` because this component's ref
   * would otherwise point at the wrapper View, not the input inside it.
   */
  inputRef?: RefObject<TextInput | null>;
  style?: StyleProp<ViewStyle>;
  inputStyle?: StyleProp<TextStyle>;
}

export function TextField({
  label,
  accessibilityLabel,
  value,
  onChangeText,
  placeholder,
  hint,
  error,
  optional = false,
  secureTextEntry = false,
  autoCapitalize = "sentences",
  autoComplete = "off",
  keyboardType = "default",
  multiline = false,
  editable = true,
  alignRight = false,
  suffix,
  onSubmitEditing,
  returnKeyType,
  inputRef,
  style,
  inputStyle,
}: TextFieldProps): ReactElement {
  const invalid = error !== undefined && error !== "";

  return (
    <View style={style}>
      <Field label={label} error={error} optional={optional} {...(hint !== undefined && { hint })}>
        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            borderWidth: 1,
            borderRadius: radius.input,
            borderColor: invalid ? colors.accent[500] : colors.surface.border,
            backgroundColor: invalid ? colors.accent[50] : colors.surface.raised,
            paddingHorizontal: spacing.md,
            minHeight: HIT_SIZE,
          }}
        >
          <TextInput
            ref={inputRef}
            value={value}
            onChangeText={onChangeText}
            editable={editable}
            secureTextEntry={secureTextEntry}
            autoCapitalize={autoCapitalize}
            autoComplete={autoComplete}
            autoCorrect={false}
            keyboardType={keyboardType}
            multiline={multiline}
            placeholder={placeholder ?? ""}
            placeholderTextColor={palette.faint}
            accessibilityLabel={accessibilityLabel ?? label}
            {...(onSubmitEditing !== undefined && { onSubmitEditing })}
            {...(returnKeyType !== undefined && { returnKeyType })}
            style={[
              {
                flex: 1,
                // 16 is the floor that stops iOS zooming the view on focus,
                // and is the smallest size that stays readable in sunlight.
                fontSize: 16,
                color: palette.strong,
                paddingVertical: multiline ? spacing.md : spacing.sm,
                ...(multiline && { minHeight: 88, textAlignVertical: "top" as const }),
                ...(alignRight && { textAlign: "right" as const }),
                ...(alignRight && { fontVariant: ["tabular-nums" as const] }),
              },
              inputStyle,
            ]}
          />
          {suffix !== undefined && (
            <Text variant="body" tone="muted" style={{ marginLeft: spacing.sm }}>
              {suffix}
            </Text>
          )}
        </View>
      </Field>
    </View>
  );
}

export interface ChoiceOption<T extends string> {
  value: T;
  label: string;
  /** A second line, for options whose consequence is not obvious. */
  description?: string;
}

export interface ChoiceFieldProps<T extends string> {
  label: string;
  value: T;
  options: readonly ChoiceOption<T>[];
  onChange: (value: T) => void;
  hint?: string | undefined;
  error?: string | undefined;
}

/** The <select> replacement: every option visible, one tap to choose. */
export function ChoiceField<T extends string>({
  label,
  value,
  options,
  onChange,
  hint,
  error,
}: ChoiceFieldProps<T>): ReactElement {
  return (
    <Field label={label} error={error} {...(hint !== undefined && { hint })}>
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.sm }}>
        {options.map((option) => {
          const selected = option.value === value;
          return (
            <Pressable
              key={option.value}
              onPress={() => onChange(option.value)}
              accessibilityRole="radio"
              accessibilityState={{ selected }}
              accessibilityLabel={option.label}
              style={({ pressed }) => ({
                minHeight: 40,
                justifyContent: "center",
                paddingHorizontal: spacing.lg,
                paddingVertical: spacing.sm,
                borderRadius: radius.pill,
                borderWidth: 1,
                borderColor: selected ? colors.primary[600] : colors.surface.border,
                backgroundColor: selected ? colors.primary[600] : colors.surface.raised,
                opacity: pressed ? 0.85 : 1,
              })}
            >
              <Text variant="label" tone={selected ? "onPrimary" : "body"}>
                {option.label}
              </Text>
              {option.description !== undefined && (
                <Text variant="caption" tone={selected ? "onPrimary" : "muted"}>
                  {option.description}
                </Text>
              )}
            </Pressable>
          );
        })}
      </View>
    </Field>
  );
}
