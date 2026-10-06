import type { IssueState } from "../../lib/types";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";

/** State picker, shadcn Select with colored dots. */
export default function StatePicker({
  states,
  value,
  onChange,
  disabled,
}: {
  states: IssueState[];
  value: string;
  onChange: (stateId: string) => void;
  disabled?: boolean;
}) {
  return (
    <Select
      value={value}
      onValueChange={(v) => {
        if (v !== null) onChange(v);
      }}
      disabled={disabled}
    >
      <SelectTrigger className="w-44">
        <SelectValue placeholder="Select state" />
      </SelectTrigger>
      <SelectContent>
        {states.map((s) => (
          <SelectItem key={s.id} value={s.id}>
            <span className="flex items-center gap-2">
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: s.color }}
                aria-hidden
              />
              {s.name}
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
