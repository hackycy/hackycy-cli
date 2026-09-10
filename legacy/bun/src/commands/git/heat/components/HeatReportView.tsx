import type { HeatReport, PathHeat } from '../types'
import process from 'node:process'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import { Box, Text } from 'ink'
import React, { useEffect } from 'react'

dayjs.extend(relativeTime)

interface HeatReportViewProps {
  report: HeatReport
  onDone: () => void
}

const COL = { rank: 4, changedAt: 20, kinds: 11 }
const FIXED_WIDTH = COL.rank + COL.changedAt + COL.kinds
const EARLIEST_COLOR = '#FFA500'
const LATEST_COLOR = '#98FB98'
const MATCH_BACKGROUND = '#FFD54A'
type TimeMark = 'earliest' | 'latest' | undefined

export function HeatReportView({ report, onDone }: HeatReportViewProps) {
  useEffect(() => {
    const timer = setTimeout(onDone, 100)
    return () => clearTimeout(timer)
  }, [onDone])

  return (
    <Box flexDirection="column">
      <Summary report={report} />
      <Text> </Text>
      <HeatTable
        rows={report.target === 'files' ? report.files : report.directories}
        pathLabel={report.target === 'files' ? 'File' : 'Directory'}
        relativeTime={report.relativeTime}
        query={report.query}
      />
      <Box marginTop={1}>
        <Text color="gray">
          Legend
          {'  '}
          <Text color={LATEST_COLOR}>#</Text>
          {' latest  '}
          <Text color={EARLIEST_COLOR}>#</Text>
          {' earliest  '}
          <Text color="yellow">M</Text>
          {' modified  '}
          <Text color="green">A</Text>
          {' added  '}
          <Text color="red">D</Text>
          {' deleted  '}
          <Text color="magenta">R</Text>
          {' renamed  '}
          <Text color="blue">C</Text>
          {' copied'}
        </Text>
      </Box>
    </Box>
  )
}

function Summary({ report }: { report: HeatReport }) {
  const count = report.target === 'files' ? report.files.length : report.directories.length
  const label = report.target === 'files'
    ? `file${count === 1 ? '' : 's'}`
    : `director${count === 1 ? 'y' : 'ies'}`
  const shouldShowCommitCount = !report.rangeLabel.includes('commit')

  return (
    <Text color="gray" dimColor>
      {report.repoName}
      {' · '}
      {report.rangeLabel}
      {shouldShowCommitCount
        ? (
            <>
              {' · '}
              {report.commitCount}
              {' '}
              commit
              {report.commitCount === 1 ? '' : 's'}
            </>
          )
        : null}
      {' · '}
      {count}
      {' '}
      {label}
    </Text>
  )
}

function HeatTable({ rows, pathLabel, relativeTime, query }: { rows: PathHeat[], pathLabel: string, relativeTime: boolean, query?: string }) {
  const pathWidth = getPathWidth()
  const timeMarks = getTimeMarks(rows)
  const normalizedQuery = query?.toLowerCase()

  return (
    <Box flexDirection="column">
      <Box flexDirection="row">
        <HeaderCell width={COL.rank}>#</HeaderCell>
        <HeaderCell width={COL.changedAt}>Changed at</HeaderCell>
        <HeaderCell width={COL.kinds}>M A D R C</HeaderCell>
        <Box width={pathWidth}>
          <Text bold color="magenta">{pathLabel}</Text>
        </Box>
      </Box>
      <Text color="gray">{'─'.repeat(FIXED_WIDTH + pathWidth)}</Text>
      {rows.length === 0
        ? <Text color="gray">No changes found.</Text>
        : rows.map((row, index) => (
            <HeatRow
              key={row.path}
              index={index}
              row={row}
              timeMark={timeMarks[index]}
              pathWidth={pathWidth}
              relativeTime={relativeTime}
              query={normalizedQuery}
            />
          ))}
    </Box>
  )
}

function HeatRow({ index, row, timeMark, pathWidth, relativeTime, query }: { index: number, row: PathHeat, timeMark: TimeMark, pathWidth: number, relativeTime: boolean, query?: string }) {
  const changedAt = formatChangedAt(row, relativeTime)

  return (
    <Box flexDirection="row">
      <Cell width={COL.rank}>
        <Text color={getRankColor(timeMark)} dimColor={!timeMark}>{String(index + 1)}</Text>
      </Cell>
      <Cell width={COL.changedAt}><Text color="#EB009B">{changedAt}</Text></Cell>
      <KindCell row={row} />
      <Box width={pathWidth}>
        <HighlightedPath path={row.path} query={query} />
      </Box>
    </Box>
  )
}

function HighlightedPath({ path: filePath, query }: { path: string, query?: string }) {
  const ranges = query ? getMatchRanges(filePath, query) : []
  const segments = splitPathSegments(filePath, ranges)

  return (
    <Text wrap="hard">
      {segments.map(segment => (
        <Text
          key={`${segment.start}:${segment.end}`}
          bold={segment.highlighted}
          color={segment.highlighted ? 'black' : segment.color}
          dimColor={segment.highlighted ? false : segment.dimColor}
          backgroundColor={segment.highlighted ? MATCH_BACKGROUND : undefined}
        >
          {segment.text}
        </Text>
      ))}
    </Text>
  )
}

interface PathSegment {
  start: number
  end: number
  text: string
  color: 'cyan' | 'gray'
  dimColor: boolean
  highlighted: boolean
}

interface MatchRange {
  start: number
  end: number
}

function getMatchRanges(filePath: string, query: string): MatchRange[] {
  const ranges: MatchRange[] = []
  const lowerPath = filePath.toLowerCase()
  let fromIndex = 0

  while (fromIndex < lowerPath.length) {
    const index = lowerPath.indexOf(query, fromIndex)
    if (index === -1)
      break

    ranges.push({ start: index, end: index + query.length })
    fromIndex = index + query.length
  }

  return ranges
}

function splitPathSegments(filePath: string, ranges: MatchRange[]): PathSegment[] {
  const boundaries = new Set<number>([0, filePath.length])
  const slashIndex = getDisplaySlashIndex(filePath)

  if (slashIndex !== -1) {
    boundaries.add(slashIndex)
    boundaries.add(slashIndex + 1)
  }

  for (const range of ranges) {
    boundaries.add(range.start)
    boundaries.add(range.end)
  }

  const points = [...boundaries].sort((a, b) => a - b)
  const segments: PathSegment[] = []

  for (let i = 0; i < points.length - 1; i += 1) {
    const start = points[i]!
    const end = points[i + 1]!
    if (start === end)
      continue

    segments.push({
      start,
      end,
      text: filePath.slice(start, end),
      color: slashIndex !== -1 && start < slashIndex ? 'cyan' : 'gray',
      dimColor: slashIndex !== -1 && start === slashIndex && end === slashIndex + 1,
      highlighted: ranges.some(range => start >= range.start && end <= range.end),
    })
  }

  return segments
}

function getDisplaySlashIndex(filePath: string): number {
  if (filePath === '.')
    return -1
  return filePath.lastIndexOf('/')
}

function getTimeMarks(rows: PathHeat[]): TimeMark[] {
  const marks: TimeMark[] = Array.from({ length: rows.length })
  let earliestIndex = -1
  let latestIndex = -1
  let earliestEpoch = Number.POSITIVE_INFINITY
  let latestEpoch = Number.NEGATIVE_INFINITY

  rows.forEach((row, index) => {
    if (row.lastChangedAtEpoch <= 0)
      return

    if (row.lastChangedAtEpoch > latestEpoch) {
      latestEpoch = row.lastChangedAtEpoch
      latestIndex = index
    }
    if (row.lastChangedAtEpoch < earliestEpoch) {
      earliestEpoch = row.lastChangedAtEpoch
      earliestIndex = index
    }
  })

  if (latestIndex !== -1)
    marks[latestIndex] = 'latest'
  if (earliestIndex !== -1 && earliestIndex !== latestIndex)
    marks[earliestIndex] = 'earliest'

  return marks
}

function getRankColor(mark: TimeMark): string {
  if (mark === 'latest')
    return LATEST_COLOR
  if (mark === 'earliest')
    return EARLIEST_COLOR
  return 'gray'
}

function formatChangedAt(row: PathHeat, relativeTime: boolean): string {
  if (!row.lastChangedAt)
    return '-'
  if (!relativeTime || row.lastChangedAtEpoch <= 0)
    return row.lastChangedAt
  return dayjs.unix(row.lastChangedAtEpoch).fromNow()
}

function HeaderCell({ width, children }: { width: number, children: React.ReactNode }) {
  return (
    <Box minWidth={width}>
      <Text bold color="magenta">{children}</Text>
    </Box>
  )
}

function Cell({ width, children }: { width: number, children: React.ReactNode }) {
  return <Box minWidth={width}>{children}</Box>
}

function KindCell({ row }: { row: PathHeat }) {
  return (
    <Cell width={COL.kinds}>
      <Text>
        <KindMark active={row.modified > 0} />
        {' '}
        <KindMark active={row.added > 0} />
        {' '}
        <KindMark active={row.deleted > 0} />
        {' '}
        <KindMark active={row.renamed > 0} />
        {' '}
        <KindMark active={row.copied > 0} />
      </Text>
    </Cell>
  )
}

function KindMark({ active }: { active: boolean }) {
  return <Text color={active ? 'green' : 'gray'}>{active ? '✓' : '-'}</Text>
}

function getPathWidth(): number {
  const columns = process.stdout.columns || 80
  return Math.max(1, columns - FIXED_WIDTH - 2)
}
