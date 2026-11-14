import { Toast, ToastAction, ToastClose, ToastDescription, ToastRegion, ToastTitle, toast } from "@ui-sdk/react/toast"
import { CircleAlertIcon, CircleCheckIcon, XIcon } from "lucide-react"
import { Spinner } from "./spinner"

export { toast }

export function Toaster() {
  return (
    <ToastRegion
      side="bottom"
      align="end"
      aria-label="Notifications"
      data-agent-hidden
      className="fixed right-[max(1rem,env(safe-area-inset-right))] bottom-[max(1rem,env(safe-area-inset-bottom))] z-50 w-[min(356px,calc(100vw-32px))]"
    >
      {(t) => (
        <Toast className="flex items-start gap-2.5 overflow-hidden rounded-md border bg-background p-3 pr-9 text-[13px] shadow-lg shadow-black/10 dark:shadow-black/50">
          <span className="mt-0.5 inline-flex shrink-0">
            {t.type === "success" && <CircleCheckIcon aria-hidden="true" className="size-4 text-success" />}
            {t.type === "error" && <CircleAlertIcon aria-hidden="true" className="size-4 text-destructive" />}
            {t.type === "loading" && <Spinner className="text-muted-foreground" />}
          </span>
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <ToastTitle className="font-[510] text-foreground" />
            <ToastDescription className="text-muted-foreground" />
            {t.action && <ToastAction className="mt-1.5 self-start text-[13px] font-[510] text-brand hover:brightness-125" />}
          </div>
          <ToastClose aria-label="Dismiss" className="absolute top-2.5 right-2.5 inline-flex size-6 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground">
            <XIcon aria-hidden="true" className="size-3.5" />
          </ToastClose>
        </Toast>
      )}
    </ToastRegion>
  )
}

function messageOf(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/**
 * Runs a request behind a loading toast that turns into a success or an
 * error. Resolves with the result, and rethrows so the caller can stop.
 */
export async function withToast<T>(work: Promise<T>, messages: { loading: string; success: string | ((value: T) => string); error?: string }): Promise<T> {
  toast.promise(work, {
    loading: messages.loading,
    success: messages.success,
    error: (e: unknown) => ({ title: messages.error ?? "Something went wrong", description: messageOf(e) }),
  })
  return work
}
