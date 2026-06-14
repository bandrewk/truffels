import { describe, it, expect } from 'vitest'

describe('useUpdateInProgress', () => {
  it('exports the useUpdateInProgress hook function', async () => {
    const mod = await import('./useUpdateInProgress')
    expect(typeof mod.useUpdateInProgress).toBe('function')
  })
})
