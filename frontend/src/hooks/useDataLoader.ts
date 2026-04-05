import { useCallback, useEffect, useState } from 'react'
import { getErrorMessage } from '../utils/error'

interface LoadOptions {
  silent?: boolean
}

interface UseDataLoaderOptions<T> {
  initialData: T
  load: () => Promise<T>
  onError?: (message: string, error: unknown) => void
  initialLoadMode?: 'blocking' | 'silent'
}

export function useDataLoader<T>({
  initialData,
  load,
  onError,
  initialLoadMode = 'blocking',
}: UseDataLoaderOptions<T>) {
  const [data, setData] = useState<T>(initialData)
  const [loading, setLoading] = useState(initialLoadMode !== 'silent')
  const [error, setError] = useState<string | null>(null)

  const run = useCallback(async (options: LoadOptions = {}) => {
    const { silent = false } = options

    if (!silent) {
      setLoading(true)
      setError(null)
    }

    try {
      const nextData = await load()
      setData(nextData)
      setError(null)
      return nextData
    } catch (err) {
      const message = getErrorMessage(err)
      if (!silent) {
        setError(message)
      }
      onError?.(message, err)
      return null
    } finally {
      if (!silent) {
        setLoading(false)
      }
    }
  }, [load, onError])

  useEffect(() => {
    void run(initialLoadMode === 'silent' ? { silent: true } : {})
  }, [initialLoadMode, run])

  const reload = useCallback(() => run(), [run])
  const reloadSilently = useCallback(() => run({ silent: true }), [run])

  return {
    data,
    setData,
    loading,
    error,
    reload,
    reloadSilently,
  }
}
