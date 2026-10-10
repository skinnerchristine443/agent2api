import { Button, DateField, DateRangePicker, Label, RangeCalendar, TimeField, type TimeValue } from '@heroui/react'

import { FilterSearchSelect } from '@/components/ui/FilterSearchSelect'
import { FilterSelect, type FilterSelectOption } from '@/components/ui/FilterSelect'
import { FilterToggle } from '@/components/ui/FilterToggle'
import { useI18n } from '@/hooks/I18nContext'
import { loadRequestIdOptions } from '@/hooks/useLogsQueries'
import {
  customRangeFromISO,
  dateValueToISO,
  ERROR_KIND_LABEL_KEYS,
  ERROR_KIND_VALUES,
  type ErrorKindFilter,
  type RequestFilter,
  type RequestLogsFilters,
  type StreamFilter,
  type TimeRange,
} from '@/lib/logsFormat'

/**
 * 请求日志筛选栏（纯展示 + 回调，不持状态）：状态 / 账号 / 模型 / 请求 ID /
 * 流式 / 错误类型 / 时间预设 + 自定义区间。全部经 `onChange` 写 URL（§7.3），
 * 默认值不落 URL。
 */
export function LogsRequestsFilterBar({
  filters,
  onChange,
  onClear,
  hasFilters,
  accountOptions,
  modelOptions,
  shownLabel,
}: {
  filters: RequestLogsFilters
  onChange: (patch: Partial<RequestLogsFilters>) => void
  onClear: () => void
  hasFilters: boolean
  accountOptions: FilterSelectOption[]
  modelOptions: FilterSelectOption[]
  shownLabel: string
}) {
  const { t } = useI18n()
  const customRange = filters.range === 'custom' ? customRangeFromISO(filters.from, filters.to) : null

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterToggle
          value={filters.status}
          onChange={(next) => onChange({ status: next as RequestFilter })}
          ariaLabel={t('logsFilterAll')}
          options={[
            { id: 'all', label: t('logsFilterAll') },
            { id: 'ok', label: t('logsFilterOk') },
            { id: 'incomplete', label: t('logsFilterIncomplete') },
            { id: 'error', label: t('logsFilterError') },
            { id: 'canceled', label: t('logsFilterCanceled') },
          ]}
        />
        <div className="flex items-center gap-2">
          {hasFilters ? (
            <Button size="sm" variant="ghost" onPress={onClear}>{t('clearFilters')}</Button>
          ) : null}
          <div className="mono text-micro text-muted">{shownLabel}</div>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <FilterSearchSelect
          ariaLabel={t('logsColAccount')}
          value={filters.account}
          onChange={(next) => onChange({ account: next })}
          options={accountOptions}
          allLabel={t('logsFilterAccountAll')}
          searchPlaceholder={t('logsSearchAccount')}
          emptyLabel={t('logsNoFilterOptions')}
        />
        <FilterSearchSelect
          ariaLabel={t('logsColModel')}
          value={filters.model}
          onChange={(next) => onChange({ model: next })}
          options={modelOptions}
          allLabel={t('logsFilterModelAll')}
          searchPlaceholder={t('logsSearchModel')}
          emptyLabel={t('logsNoFilterOptions')}
        />
        <FilterSearchSelect
          ariaLabel={t('logsSearchRequestId')}
          value={filters.id}
          onChange={(next) => onChange({ id: next })}
          options={filters.id ? [{ id: filters.id, label: filters.id }] : []}
          allLabel={t('logsFilterIdAll')}
          searchPlaceholder={t('logsSearchRequestId')}
          emptyLabel={t('logsNoFilterOptions')}
          loadOptions={loadRequestIdOptions}
          className="min-w-56"
        />
        <FilterToggle
          value={filters.stream}
          onChange={(next) => onChange({ stream: next as StreamFilter })}
          ariaLabel={t('logsFilterStreamAll')}
          options={[
            { id: 'all', label: t('logsFilterStreamAll') },
            { id: 'stream', label: t('logsStreamYes') },
            { id: 'sync', label: t('logsStreamNo') },
          ]}
        />
        <FilterSelect
          ariaLabel={t('errorKind')}
          value={filters.kind === 'all' ? '' : filters.kind}
          onChange={(next) => onChange({ kind: (next || 'all') as ErrorKindFilter })}
          options={[
            { id: '', label: t('logsFilterKindAll') },
            ...ERROR_KIND_VALUES.map((kind) => ({ id: kind, label: t(ERROR_KIND_LABEL_KEYS[kind]) })),
          ]}
        />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs font-medium text-muted">{t('logsTimeRange')}</span>
        <FilterToggle
          value={filters.range}
          onChange={(next) => onChange({ range: next as TimeRange })}
          ariaLabel={t('logsTimeRange')}
          options={[
            { id: 'all', label: t('logsTimeAll') },
            { id: '1h', label: t('logsTime1h') },
            { id: '24h', label: t('logsTime24h') },
            { id: '7d', label: t('logsTime7d') },
            { id: 'custom', label: t('logsTimeCustom') },
          ]}
        />
        {filters.range === 'custom' ? (
          <DateRangePicker
            className="w-[min(100%,42rem)]"
            granularity="minute"
            hourCycle={24}
            shouldForceLeadingZeros
            value={customRange}
            onChange={(next) => onChange({
              range: 'custom',
              from: dateValueToISO(next?.start) ?? '',
              to: dateValueToISO(next?.end, true) ?? '',
            })}
            aria-label={t('logsTimeRange')}
          >
            {({ state }) => (
              <>
                <DateField.Group className="w-full min-w-0" variant="secondary">
                  <DateField.Input className="min-w-0" slot="start" aria-label={t('logsTimeFrom')}>
                    {(segment) => <DateField.Segment segment={segment} />}
                  </DateField.Input>
                  <DateRangePicker.RangeSeparator />
                  <DateField.Input className="min-w-0" slot="end" aria-label={t('logsTimeTo')}>
                    {(segment) => <DateField.Segment segment={segment} />}
                  </DateField.Input>
                  <DateField.Suffix>
                    <DateRangePicker.Trigger>
                      <DateRangePicker.TriggerIndicator />
                    </DateRangePicker.Trigger>
                  </DateField.Suffix>
                </DateField.Group>
                <DateRangePicker.Popover className="flex w-[20.5rem] max-w-[calc(100vw-2rem)] flex-col gap-3 p-3">
                  <RangeCalendar aria-label={t('logsTimeRange')}>
                    <RangeCalendar.Header>
                      <RangeCalendar.YearPickerTrigger>
                        <RangeCalendar.YearPickerTriggerHeading />
                        <RangeCalendar.YearPickerTriggerIndicator />
                      </RangeCalendar.YearPickerTrigger>
                      <RangeCalendar.NavButton slot="previous" />
                      <RangeCalendar.NavButton slot="next" />
                    </RangeCalendar.Header>
                    <RangeCalendar.Grid>
                      <RangeCalendar.GridHeader>
                        {(day) => (
                          <RangeCalendar.HeaderCell>{day}</RangeCalendar.HeaderCell>
                        )}
                      </RangeCalendar.GridHeader>
                      <RangeCalendar.GridBody>
                        {(date) => <RangeCalendar.Cell date={date} />}
                      </RangeCalendar.GridBody>
                    </RangeCalendar.Grid>
                    <RangeCalendar.YearPickerGrid>
                      <RangeCalendar.YearPickerGridBody>
                        {({ year }) => <RangeCalendar.YearPickerCell year={year} />}
                      </RangeCalendar.YearPickerGridBody>
                    </RangeCalendar.YearPickerGrid>
                  </RangeCalendar>
                  <div className="flex flex-col gap-3">
                    <div className="flex items-center justify-between gap-3">
                      <Label className="text-xs">{t('logsTimeFrom')}</Label>
                      <TimeField
                        aria-label={t('logsTimeFrom')}
                        granularity="minute"
                        hourCycle={24}
                        value={state.timeRange?.start ?? null}
                        onChange={(value) =>
                          state.setTimeRange({
                            start: value as TimeValue,
                            end: state.timeRange?.end as TimeValue,
                          })
                        }
                      >
                        <TimeField.Group className="w-[7.5rem]" variant="secondary">
                          <TimeField.Input>
                            {(segment) => <TimeField.Segment segment={segment} />}
                          </TimeField.Input>
                        </TimeField.Group>
                      </TimeField>
                    </div>
                    <div className="flex items-center justify-between gap-3">
                      <Label className="text-xs">{t('logsTimeTo')}</Label>
                      <TimeField
                        aria-label={t('logsTimeTo')}
                        granularity="minute"
                        hourCycle={24}
                        value={state.timeRange?.end ?? null}
                        onChange={(value) =>
                          state.setTimeRange({
                            start: state.timeRange?.start as TimeValue,
                            end: value as TimeValue,
                          })
                        }
                      >
                        <TimeField.Group className="w-[7.5rem]" variant="secondary">
                          <TimeField.Input>
                            {(segment) => <TimeField.Segment segment={segment} />}
                          </TimeField.Input>
                        </TimeField.Group>
                      </TimeField>
                    </div>
                  </div>
                </DateRangePicker.Popover>
              </>
            )}
          </DateRangePicker>
        ) : null}
      </div>
    </div>
  )
}
