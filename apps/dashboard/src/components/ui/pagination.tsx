import {
  PaginationEllipsis,
  PaginationItem,
  PaginationLink,
  PaginationList,
  PaginationNext,
  Pagination as PaginationPart,
  PaginationPrevious,
} from "@ui-sdk/react/pagination"
import { ChevronLeftIcon, ChevronRightIcon } from "lucide-react"
import type { ComponentProps } from "react"
import { cn } from "@/lib/utils"
import { buttonVariants } from "./button"

type PaginationProps = Omit<ComponentProps<typeof PaginationPart>, "children" | "className"> & {
  className?: string
  label?: string
}

const step = buttonVariants({ variant: "ghost", size: "icon", className: "data-disabled:opacity-40" })

export function Pagination({ className, label = "Pages", ...props }: PaginationProps) {
  return (
    <PaginationPart aria-label={label} className={cn("flex", className)} {...props}>
      {(state) => (
        <PaginationList className="flex items-center gap-1">
          <PaginationItem>
            <PaginationPrevious aria-label="Previous page" className={step}>
              <ChevronLeftIcon aria-hidden="true" />
            </PaginationPrevious>
          </PaginationItem>
          {state.items.map((item) => (
            <PaginationItem key={item}>
              {typeof item === "number" ? (
                <PaginationLink
                  page={item}
                  className={buttonVariants({ variant: "ghost", size: "icon", className: "tabular-nums data-current:bg-accent data-current:text-foreground" })}
                />
              ) : (
                <PaginationEllipsis className="inline-flex size-8 items-center justify-center text-faint" />
              )}
            </PaginationItem>
          ))}
          <PaginationItem>
            <PaginationNext aria-label="Next page" className={step}>
              <ChevronRightIcon aria-hidden="true" />
            </PaginationNext>
          </PaginationItem>
        </PaginationList>
      )}
    </PaginationPart>
  )
}
