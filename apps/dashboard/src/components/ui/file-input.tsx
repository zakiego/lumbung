import { FileInputTrigger as FileInputTriggerPart } from "@ui-sdk/react/file-input"
import { type Styled, cn } from "@/lib/utils"
import { buttonVariants } from "./button"

export { FileInput, FileInputDropzone, createFileInputController, formatFileSize } from "@ui-sdk/react/file-input"

export function FileInputTrigger({ className, ...props }: Styled<typeof FileInputTriggerPart>) {
  return <FileInputTriggerPart className={buttonVariants({ variant: "secondary", className: cn("data-invalid:border-destructive", className) })} {...props} />
}
