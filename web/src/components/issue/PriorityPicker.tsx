import { PRIORITY_LABELS } from "../../lib/types";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";

/** Priority picker (0–4), shadcn Select. */
export default function PriorityPicker({
  value,
  onChange,
  disabled,
}: {
  value: number;
  onChange: (v: number) => void;
  disabled?: boolean;
}) {
  return (
    <Select
      value={String(value)}
      onValueChange={(v) => onChange(Number(v))}
      disabled={disabled}
    >
      <SelectTrigger className="w-32">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {PRIORITY_LABELS.map((label, i) => (
          <SelectItem key={i} value={String(i)}>
            {label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
