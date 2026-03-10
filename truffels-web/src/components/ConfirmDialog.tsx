import { ReactNode, useEffect, useCallback } from 'react'

interface ConfirmDialogProps {
  open: boolean
  title: string
  onConfirm: () => void
  onCancel: () => void
  confirmLabel?: string
  confirmDisabled?: boolean
  children: ReactNode
}

export default function ConfirmDialog({
  open,
  title,
  onConfirm,
  onCancel,
  confirmLabel = 'Confirm',
  confirmDisabled = false,
  children,
}: ConfirmDialogProps) {
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel()
    },
    [onCancel],
  )

  useEffect(() => {
    if (open) {
      document.addEventListener('keydown', handleKeyDown)
      return () => document.removeEventListener('keydown', handleKeyDown)
    }
  }, [open, handleKeyDown])

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={onCancel}
    >
      <div
        className="bg-surface-raised border border-border rounded-lg p-6 w-full max-w-md mx-4 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-lg font-semibold text-gray-200 mb-4">{title}</h2>

        <div className="mb-6">{children}</div>

        <div className="flex justify-end gap-3">
          <button
            onClick={onCancel}
            className="px-4 py-2 text-sm rounded bg-surface-overlay hover:bg-surface text-gray-400 transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={confirmDisabled}
            className="px-4 py-2 text-sm rounded bg-accent/20 hover:bg-accent/30 text-accent transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
