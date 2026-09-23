import { Flex, IconButton, TextField } from "@radix-ui/themes";
import { Minus, Plus } from "lucide-react";
import React from "react";

interface NumberPickerProps {
  defaultValue?: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  [key: string]: any; // For additional props
}

export default function NumberPicker({
  defaultValue,
  onChange,
  min = 1,
  max = 100,
  ...props
}: NumberPickerProps) {
  const initialValue = defaultValue !== undefined ? defaultValue : min;
  const clampedInitial = Math.max(min, Math.min(max, initialValue));
  const [value, setValue] = React.useState(String(clampedInitial));

  // `defaultValue` is the prop the parent controls; the picker owns the draft
  // the user is typing. A change of the prop re-syncs the draft, and the
  // adjustment happens during render (guarded on the props it mirrors, so it
  // converges) instead of in an effect. Two things this deliberately does not
  // do, both of which the previous effect did:
  //
  //  * it does not depend on `onChange`, so a parent that re-renders — even one
  //    that passes a fresh inline arrow, as `pages/admin/log.tsx` did — cannot
  //    make the picker re-sync or re-fire;
  //  * it does not echo the change back through `onChange`. `onChange` is a
  //    report of a user edit; a `defaultValue` change is the parent telling the
  //    picker what it already knows, so echoing it just re-entered the parent
  //    with the value it had sent. Dropping the echo is what makes pagination
  //    on the admin log page stop resetting to page 1.
  const [synced, setSynced] = React.useState<{
    defaultValue: number | undefined;
    min: number;
    max: number;
  } | null>(() => ({ defaultValue, min, max }));
  if (
    defaultValue !== undefined &&
    synced !== null &&
    (defaultValue !== synced.defaultValue ||
      min !== synced.min ||
      max !== synced.max)
  ) {
    setSynced({ defaultValue, min, max });
    setValue(String(Math.max(min, Math.min(max, defaultValue))));
  }

  const handleChange = (newValue: number) => {
    const clampedValue = Math.max(min, Math.min(max, newValue));
    setValue(String(clampedValue));
    onChange(clampedValue);
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const inputValue = e.target.value.trim();
    setValue(inputValue);

    // Only trigger onChange for valid numbers within range
    const numValue = Number(inputValue);
    if (!isNaN(numValue)) {
      if (numValue >= min && numValue <= max) {
        onChange(numValue);
      }
    }
  };

  const handleBlur = () => {
    const numValue = Number(value);
    if (value === "") {
      setValue(String(min));
      onChange(min);
      return;
    }

    const clampedValue = Math.max(min, Math.min(max, numValue));
    setValue(String(clampedValue));
    onChange(clampedValue);
  };

  const currentValue = Number(value) || min;
  const isMinDisabled = currentValue <= min;
  const isMaxDisabled = currentValue >= max;

  return (
    <Flex align="center" gap="2" className="km-ui-number-picker">
      <IconButton
        variant="soft"
        radius="full"
        size="1"
        onClick={() => handleChange(currentValue - 1)}
        disabled={isMinDisabled}
        aria-label="decrement"
      >
        <Minus size="16" />
      </IconButton>
      <TextField.Root
        className="km-ui-number-input"
        type="text"
        inputMode="numeric"
        value={value}
        onChange={handleInputChange}
        onBlur={handleBlur}
        style={{ width: "4rem", textAlign: "center" }}
        {...props}
      >
        <TextField.Slot />
        <TextField.Slot />
      </TextField.Root>
      <IconButton
        variant="soft"
        radius="full"
        size="1"
        onClick={() => handleChange(currentValue + 1)}
        disabled={isMaxDisabled}
        aria-label="increment"
      >
        <Plus size="16" />
      </IconButton>
    </Flex>
  );
}
