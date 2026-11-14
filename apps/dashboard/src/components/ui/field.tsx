import {
  FieldDescription as FieldDescriptionPart,
  FieldError as FieldErrorPart,
  FieldLabel as FieldLabelPart,
  Field as FieldPart,
} from "@ui-sdk/react/field"
import { Label as LabelPart } from "@ui-sdk/react/label"
import { type Styled, cn } from "@/lib/utils"

export function Field({ className, ...props }: Styled<typeof FieldPart>) {
  return <FieldPart className={cn("flex flex-col gap-1.5", className)} {...props} />
}

const label = "text-[13px] font-[510] text-foreground data-disabled:opacity-50"

export function FieldLabel({ className, ...props }: Styled<typeof FieldLabelPart>) {
  return <FieldLabelPart className={cn(label, className)} {...props} />
}

export function FieldDescription({ className, ...props }: Styled<typeof FieldDescriptionPart>) {
  return <FieldDescriptionPart className={cn("text-xs text-muted-foreground", className)} {...props} />
}

export function FieldError({ className, ...props }: Styled<typeof FieldErrorPart>) {
  return <FieldErrorPart className={cn("text-xs text-destructive", className)} {...props} />
}

export function Label({ className, ...props }: Styled<typeof LabelPart>) {
  return <LabelPart className={cn(label, className)} {...props} />
}
