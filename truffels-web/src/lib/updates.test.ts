import { describe, it, expect } from 'vitest'
import { truncDigest, displayVersion, canRollback, formatTime, logStatusMap, parseVersion, compareVersion } from './updates'

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

describe('displayVersion', () => {
  it('returns em-dash for an unknown version', () => {
    // dev.25: checkService leaves current_version empty for a build whose
    // image carries no source ref. Rendering it raw left the arrow with
    // nothing on its left side.
    expect(displayVersion('')).toBe('—')
    expect(displayVersion(undefined)).toBe('—')
    expect(displayVersion(null)).toBe('—')
  })

  it('passes a known version through unchanged', () => {
    expect(displayVersion('8f2e7c2f1a2b')).toBe('8f2e7c2f1a2b')
    expect(displayVersion('v1.2.0')).toBe('v1.2.0')
  })
})

describe('canRollback', () => {
  it('offers a rollback when the running version is known and differs', () => {
    expect(canRollback({ floatingTag: false, fromVersion: 'v1.0.0', currentVersion: 'v1.2.0' })).toBe(true)
  })

  it('refuses when the running version is unknown', () => {
    // dev.25: the whole point of leaving current_version empty is that we
    // cannot prove what runs. A rollback whose starting point is unknown is
    // exactly the guess the source-ref label check exists to prevent — and
    // the old `from_version !== current_version` compared truthy against '',
    // so the button appeared for every unlabelled ckpool/ckstats build.
    expect(canRollback({ floatingTag: false, fromVersion: 'v1.0.0', currentVersion: '' })).toBe(false)
    expect(canRollback({ floatingTag: false, fromVersion: 'v1.0.0', currentVersion: undefined })).toBe(false)
    expect(canRollback({ floatingTag: false, fromVersion: 'v1.0.0', currentVersion: null })).toBe(false)
  })

  it('refuses when there is nothing to roll back to', () => {
    expect(canRollback({ floatingTag: false, fromVersion: '', currentVersion: 'v1.2.0' })).toBe(false)
    expect(canRollback({ floatingTag: false, fromVersion: undefined, currentVersion: 'v1.2.0' })).toBe(false)
  })

  it('refuses when the service already runs that version', () => {
    expect(canRollback({ floatingTag: false, fromVersion: 'v1.0.0', currentVersion: 'v1.0.0' })).toBe(false)
  })

  it('refuses for floating-tag services', () => {
    // The old image is overwritten in place — there is nothing to restore.
    expect(canRollback({ floatingTag: true, fromVersion: 'v1.0.0', currentVersion: 'v1.2.0' })).toBe(false)
  })
})

describe('canRollback with an absent floating_tag', () => {
  it('treats a missing flag as not floating', () => {
    // ServiceTemplate.floating_tag is optional in the API type, so the guard
    // must not turn `undefined` into a refusal.
    expect(canRollback({ fromVersion: 'v1.0.0', currentVersion: 'v1.2.0' })).toBe(true)
  })
})
