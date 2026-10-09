// EstimateSelect (C7T1): shadcn Select over every estimate scale's points.
// The value is a point id; "" = no estimate (default).

import type { Estimate } from "../../lib/taxonomy";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";

export default function EstimateSelect({
  estimates,
  value,
  onChange,
  disabled,
}: {
  estimates: Estimate[];
  value: string;
  onChange: (pointId: string) => void;
  disabled?: boolean;
}) {
  const options = estimates.flatMap((e) =>
    e.points.map((p) => ({
      id: p.id,
      label: `${e.name} · ${p.key}`,
    })),
  );
  return (
    <Select
      value={value || undefined}
      onValueChange={(v) => onChange(v === "__none" || v === null ? "" : v)}
      disabled={disabled}
    >
      <SelectTrigger className="w-44">
        <SelectValue placeholder="No estimate" />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="__none">No estimate</SelectItem>
        {options.map((o) => (
          <SelectItem key={o.id} value={o.id}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
