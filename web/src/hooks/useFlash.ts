import { useEffect, useRef, useState } from 'react'

/** Returns true for ~500ms whenever `value` changes. */
export function useFlash(value: unknown): boolean {
  const [flashing, setFlashing] = useState(false)
  const prev = useRef<unknown>(undefined)

  useEffect(() => {
    if (prev.current !== undefined && prev.current !== value) {
      prev.current = value
      setFlashing(true)
      const t = setTimeout(() => setFlashing(false), 500)
      return () => clearTimeout(t)
    }
    prev.current = value
  }, [value])

  return flashing
}
