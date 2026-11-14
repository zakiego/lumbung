// Previous and Next only, for lists with more pages than numbered links fit,
// like a table with millions of rows.
import { PaginationItem, PaginationList, PaginationNext, Pagination as PaginationPart, PaginationPrevious } from "@ui-sdk/react/pagination"
import { ChevronLeftIcon, ChevronRightIcon } from "lucide-react"
import type { ComponentProps } from "react"
import { cn } from "@/lib/utils"
import { buttonVariants } from "./button"

type PagerProps = Omit<ComponentProps<typeof PaginationPart>, "children" | "className"> & {
  className?: string
  label: string
}

const step = buttonVariants({ variant: "ghost", size: "icon", className: "data-disabled:opacity-40" })

export function Pager({ className, label, ...props }: PagerProps) {
  return (
    <PaginationPart aria-label={label} className={cn("flex", className)} {...props}>
      <PaginationList className="flex items-center gap-1">
        <PaginationItem>
          <PaginationPrevious aria-label="Previous page" className={step}>
            <ChevronLeftIcon aria-hidden="true" className="rtl:rotate-180" />
          </PaginationPrevious>
        </PaginationItem>
        <PaginationItem>
          <PaginationNext aria-label="Next page" className={step}>
            <ChevronRightIcon aria-hidden="true" className="rtl:rotate-180" />
          </PaginationNext>
        </PaginationItem>
      </PaginationList>
    </PaginationPart>
  )
}
