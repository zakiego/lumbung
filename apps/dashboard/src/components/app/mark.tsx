export function Mark({ className = "size-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true" fill="currentColor">
      <path d="M16 3 3 13h4v9h18v-9h4L16 3Zm-5 12h10v5H11v-5Zm-2 9h2v5H9v-5Zm12 0h2v5h-2v-5Z" />
    </svg>
  )
}
