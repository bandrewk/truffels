import { describe, it, expect } from 'vitest'
import { truncDigest, formatTime, logStatusMap, parseVersion, compareVersion } from './updates'

describe('truncDigest', () => {
  it('returns em-dash for empty string', () => {
    expect(truncDigest('')).toBe('—')
  })

  it('returns em-dash for undefined-like falsy', () => {
    expect(truncDigest(undefined as unknown as string)).toBe('—')
  })

  it('truncates sha256 digest to 19 chars + ellipsis', () => {
    const digest = 'sha256:abc123def456789012345678901234567890123456789012345678901234abcd'
    expect(truncDigest(digest)).toBe('sha256:abc123def456…')
    expect(truncDigest(digest).length).toBe(20) // 19 chars + ellipsis
  })

  it('returns short sha256 digest truncated', () => {
    expect(truncDigest('sha256:abcdef')).toBe('sha256:abcdef…')
  })

  it('returns non-digest version as-is', () => {
    expect(truncDigest('v3.2.1')).toBe('v3.2.1')
  })

  it('returns tag version as-is', () => {
    expect(truncDigest('30.2')).toBe('30.2')
  })

  it('returns commit hash as-is', () => {
    expect(truncDigest('a1b2c3d4e5f6')).toBe('a1b2c3d4e5f6')
  })
})

describe('formatTime', () => {
  it('returns empty string for empty input', () => {
    expect(formatTime('')).toBe('')
  })

  it('returns original string for invalid date', () => {
    expect(formatTime('not-a-date')).toBe('not-a-date')
  })

  it('formats valid ISO date', () => {
    const result = formatTime('2026-03-10T14:30:00Z')
    // Should contain date components (locale-dependent but de-DE uses dd.mm.yy)
    expect(result).toBeTruthy()
    expect(result.length).toBeGreaterThan(0)
  })

  it('formats ISO date with timezone', () => {
    const result = formatTime('2026-03-10T14:30:00+01:00')
    expect(result).toBeTruthy()
  })
})

describe('logStatusMap', () => {
  it('maps done to running', () => {
    expect(logStatusMap('done')).toBe('running')
  })

  it('maps failed to critical', () => {
    expect(logStatusMap('failed')).toBe('critical')
  })

  it('maps rolled_back to warning', () => {
    expect(logStatusMap('rolled_back')).toBe('warning')
  })

  it('maps pulling to degraded', () => {
    expect(logStatusMap('pulling')).toBe('degraded')
  })

  it('maps building to degraded', () => {
    expect(logStatusMap('building')).toBe('degraded')
  })

  it('maps restarting to degraded', () => {
    expect(logStatusMap('restarting')).toBe('degraded')
  })

  it('maps pending to unknown', () => {
    expect(logStatusMap('pending')).toBe('unknown')
  })

  it('maps empty string to unknown', () => {
    expect(logStatusMap('')).toBe('unknown')
  })

  it('maps unrecognized status to unknown', () => {
    expect(logStatusMap('something_else')).toBe('unknown')
  })
})

describe('parseVersion', () => {
  it('strips leading v prefix', () => {
    expect(parseVersion('v31.0')).toEqual([31, 0])
  })

  it('strips tag-variant suffix', () => {
    expect(parseVersion('31.0-arm64')).toEqual([31, 0])
    expect(parseVersion('16.14-alpine')).toEqual([16, 14])
  })

  it('returns empty array for non-numeric input', () => {
    expect(parseVersion('a01fcb3e0a53')).toEqual([])
  })

  it('parses multi-segment semver', () => {
    expect(parseVersion('v0.3.1')).toEqual([0, 3, 1])
  })
})

describe('compareVersion', () => {
  it('returns positive for upgrade', () => {
    expect(compareVersion('31.1', '31.0')).toBeGreaterThan(0)
    expect(compareVersion('v3.3.1', 'v3.2.1')).toBeGreaterThan(0)
  })

  it('returns negative for downgrade', () => {
    expect(compareVersion('30.2', '31.0')).toBeLessThan(0)
  })

  it('returns 0 for tag variants of the same semver', () => {
    // dev.20: 31.0 and 31.0-arm64 are the same release, distinct images.
    // compareVersion returning 0 is correct; the UI surfaces a "Switch to"
    // button when the strings differ.
    expect(compareVersion('31.0-arm64', '31.0')).toBe(0)
    expect(compareVersion('31.0-arm32', '31.0-arm64')).toBe(0)
  })

  it('returns 0 for alpine variant of same semver', () => {
    expect(compareVersion('16.14-alpine', '16.14')).toBe(0)
  })

  it('treats v-prefix as equivalent', () => {
    expect(compareVersion('v31.0', '31.0')).toBe(0)
  })
})
