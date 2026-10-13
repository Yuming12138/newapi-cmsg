/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { StrictMode, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'
import { I18nextProvider } from 'react-i18next'

// Run real React/DOM interactions locally, without a browser or network access.
const dom = new Window({ url: 'http://localhost' })
const domGlobals = {
  window: dom,
  document: dom.document,
  navigator: dom.navigator,
  HTMLElement: dom.HTMLElement,
  HTMLInputElement: dom.HTMLInputElement,
  HTMLTextAreaElement: dom.HTMLTextAreaElement,
  Node: dom.Node,
  Event: dom.Event,
  MouseEvent: dom.MouseEvent,
  FocusEvent: dom.FocusEvent,
  MutationObserver: dom.MutationObserver,
  getComputedStyle: dom.getComputedStyle.bind(dom),
  IS_REACT_ACT_ENVIRONMENT: true,
}
const previousGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const [name, value] of Object.entries(domGlobals)) {
  previousGlobals.set(name, Object.getOwnPropertyDescriptor(globalThis, name))
  Object.defineProperty(globalThis, name, {
    configurable: true,
    writable: true,
    value,
  })
}

const { act, cleanup, fireEvent, render } =
  await import('@testing-library/react')
const { ModelMappingEditor } = await import('../model-mapping-editor')
const i18n = createInstance()
await i18n.init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
})

const mappings = {
  'gpt-5.5': 'kimi-k2.6',
  'gpt-5.6-sol': 'kimi-k3',
  'gpt-5.6-terra': 'kimi-k2.7-code',
  'gpt-5.6-luna': 'kimi-k2.7-code-highspeed',
  'gpt-6.1-sol': 'kimi-k3',
}
const initialValue = JSON.stringify(mappings, null, 2)

function ControlledEditor(props: {
  initialValue: string
  onChange: (value: string) => void
}) {
  const [value, setValue] = useState(props.initialValue)
  return (
    <ModelMappingEditor
      value={value}
      onChange={(nextValue) => {
        setValue(nextValue)
        props.onChange(nextValue)
      }}
    />
  )
}

function renderControlled(value = initialValue) {
  const emittedValues: string[] = []
  const view = render(
    <StrictMode>
      <I18nextProvider i18n={i18n}>
        <ControlledEditor
          initialValue={value}
          onChange={(nextValue) => emittedValues.push(nextValue)}
        />
      </I18nextProvider>
    </StrictMode>
  )
  return { ...view, emittedValues }
}

function HookFormEditor() {
  const form = useForm({ defaultValues: { model_mapping: initialValue } })
  const mapping = form.watch('model_mapping')
  return (
    <div data-current-mapping={mapping}>
      <Controller
        control={form.control}
        name='model_mapping'
        render={({ field }) => (
          <ModelMappingEditor value={field.value} onChange={field.onChange} />
        )}
      />
    </div>
  )
}

function inputs(view: ReturnType<typeof render>): HTMLInputElement[] {
  return view.getAllByRole('textbox') as HTMLInputElement[]
}

function focus(input: HTMLInputElement) {
  act(() => input.focus())
  assert.ok(document.activeElement === input, 'input should keep focus')
}

function assertInputIdentity(
  actual: HTMLInputElement[],
  expected: HTMLInputElement[]
) {
  assert.equal(actual.length, expected.length)
  assert.ok(
    actual.every((input, index) => input === expected[index]),
    'editing should preserve the original input DOM nodes'
  )
}

function deleteButton(input: HTMLInputElement): HTMLButtonElement {
  const button = input.parentElement?.querySelector('button')
  assert.ok(button)
  return button
}

afterEach(() => cleanup())
after(async () => {
  await dom.happyDOM.abort()
  for (const [name, descriptor] of previousGlobals) {
    if (descriptor) {
      Object.defineProperty(globalThis, name, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, name)
    }
  }
})

describe('model mapping editor input identity', () => {
  test('keeps focus with the actual watched React Hook Form parent', () => {
    const view = render(
      <StrictMode>
        <I18nextProvider i18n={i18n}>
          <HookFormEditor />
        </I18nextProvider>
      </StrictMode>
    )
    const originalInputs = inputs(view)
    const input = originalInputs[0]
    focus(input)
    for (const character of '-updated') {
      fireEvent.change(input, { target: { value: input.value + character } })
      assertInputIdentity(inputs(view), originalInputs)
      assert.ok(document.activeElement === input, 'input should keep focus')
    }
    const watchedMapping = view.container
      .querySelector('[data-current-mapping]')!
      .getAttribute('data-current-mapping')!
    assert.equal(JSON.parse(watchedMapping)['gpt-5.5-updated'], 'kimi-k2.6')
  })

  test('preserves the caret when editing inside a model name', () => {
    const view = renderControlled()
    const input = inputs(view)[0]
    focus(input)
    fireEvent.change(input, {
      target: { value: 'gpt-6.1-sol', selectionStart: 7, selectionEnd: 7 },
    })
    assert.ok(inputs(view)[0] === input, 'input should not remount')
    assert.ok(document.activeElement === input, 'input should keep focus')
    assert.equal(input.selectionStart, 7)
    assert.equal(input.selectionEnd, 7)
  })

  test('keeps the source input, focus and other rows during continuous typing', () => {
    const view = renderControlled()
    const originalInputs = inputs(view)
    const input = originalInputs[0]
    focus(input)

    for (const character of '-updated') {
      const nextValue = input.value + character
      fireEvent.change(input, { target: { value: nextValue } })
      assertInputIdentity(inputs(view), originalInputs)
      assert.ok(document.activeElement === input, 'input should keep focus')
      assert.equal(input.value, nextValue)
      assert.equal(input.selectionStart, nextValue.length)
    }

    assert.equal(
      JSON.parse(view.emittedValues.at(-1)!)['gpt-5.5-updated'],
      'kimi-k2.6'
    )
  })

  test('keeps the replacement input and focus during continuous typing', () => {
    const view = renderControlled()
    const input = inputs(view)[1]
    focus(input)
    for (const character of '-updated') {
      fireEvent.change(input, { target: { value: input.value + character } })
      assert.ok(
        inputs(view)[1] === input,
        'replacement input should not remount'
      )
      assert.ok(document.activeElement === input, 'input should keep focus')
    }
    assert.equal(
      JSON.parse(view.emittedValues.at(-1)!)['gpt-5.5'],
      'kimi-k2.6-updated'
    )
  })

  test('keeps a newly added row after its first character is echoed by the form', () => {
    const view = renderControlled()
    const existingInputs = inputs(view)
    fireEvent.click(view.getByRole('button', { name: 'Add Mapping' }))
    const input = inputs(view).at(-2)!
    focus(input)
    for (const character of 'gpt-6-sol') {
      fireEvent.change(input, { target: { value: input.value + character } })
      assert.ok(inputs(view).at(-2) === input, 'new input should not remount')
      assertInputIdentity(inputs(view).slice(0, 10), existingInputs)
      assert.ok(document.activeElement === input, 'input should keep focus')
    }
  })

  test('preserves a cleared source so it can be retyped', () => {
    const view = renderControlled()
    const input = inputs(view)[0]
    focus(input)
    fireEvent.change(input, { target: { value: '' } })
    assert.equal(inputs(view).length, 10)
    assert.ok(inputs(view)[0] === input, 'cleared input should not remount')
    assert.ok(document.activeElement === input, 'input should keep focus')
    assert.equal(
      Object.hasOwn(JSON.parse(view.emittedValues.at(-1)!), 'gpt-5.5'),
      false
    )
    fireEvent.change(input, { target: { value: 'gpt-6-sol' } })
    assert.ok(inputs(view)[0] === input, 'retyped input should not remount')
    assert.equal(
      JSON.parse(view.emittedValues.at(-1)!)['gpt-6-sol'],
      'kimi-k2.6'
    )
  })

  test('allows filling the replacement before the source, even after JSON normalization', () => {
    const view = renderControlled(JSON.stringify(mappings))
    fireEvent.click(view.getByRole('button', { name: 'Add Mapping' }))
    const target = inputs(view).at(-1)!
    focus(target)
    fireEvent.change(target, { target: { value: 'kimi-k3' } })
    assert.equal(inputs(view).length, 12)
    assert.ok(inputs(view).at(-1) === target, 'target draft should not remount')
    assert.ok(document.activeElement === target, 'input should keep focus')
    fireEvent.change(inputs(view).at(-2)!, { target: { value: 'gpt-6-sol' } })
    assert.equal(JSON.parse(view.emittedValues.at(-1)!)['gpt-6-sol'], 'kimi-k3')
  })

  test('preserves extra blank rows and deletes only the requested row', () => {
    const view = renderControlled()
    fireEvent.click(view.getByRole('button', { name: 'Add Mapping' }))
    fireEvent.click(view.getByRole('button', { name: 'Add Mapping' }))
    const before = inputs(view)
    fireEvent.change(before[1], { target: { value: 'kimi-k3-updated' } })
    assertInputIdentity(inputs(view), before)
    fireEvent.click(deleteButton(before[4]))
    assertInputIdentity(
      inputs(view),
      before.filter((_, index) => index !== 4 && index !== 5)
    )
    assert.equal(
      Object.hasOwn(JSON.parse(view.emittedValues.at(-1)!), 'gpt-5.6-terra'),
      false
    )
  })

  test('gives rapid additions distinct identities even within the same millisecond', () => {
    const originalNow = Date.now
    Date.now = () => 1000
    try {
      const view = renderControlled('')
      fireEvent.click(view.getByRole('button', { name: 'Add Mapping' }))
      fireEvent.click(view.getByRole('button', { name: 'Add Mapping' }))
      const before = inputs(view)
      fireEvent.change(before[0], { target: { value: 'gpt-6-sol' } })
      assert.equal(inputs(view).length, 4)
      assert.equal(inputs(view)[2].value, '')
      fireEvent.click(deleteButton(before[0]))
      assertInputIdentity(inputs(view), before.slice(2))
    } finally {
      Date.now = originalNow
    }
  })

  test('preserves unfinished rows when switching modes without editing JSON', () => {
    const view = renderControlled()
    fireEvent.click(view.getByRole('button', { name: 'Add Mapping' }))
    fireEvent.change(inputs(view).at(-1)!, {
      target: { value: 'kimi-k3-draft' },
    })
    fireEvent.click(view.getByRole('button', { name: 'JSON Mode' }))
    fireEvent.click(view.getByRole('button', { name: 'Visual Mode' }))
    assert.equal(inputs(view).length, 12)
    assert.equal(inputs(view).at(-2)!.value, '')
    assert.equal(inputs(view).at(-1)!.value, 'kimi-k3-draft')
  })

  test('retains the JSON textarea through invalid drafts and applies a valid edit', () => {
    const view = renderControlled()
    fireEvent.click(view.getByRole('button', { name: 'JSON Mode' }))
    const textarea = view.getByRole('textbox') as HTMLTextAreaElement
    act(() => textarea.focus())
    fireEvent.change(textarea, { target: { value: '{' } })
    assert.ok(
      view.getByRole('textbox') === textarea,
      'textarea should not remount'
    )
    assert.ok(document.activeElement === textarea, 'textarea should keep focus')
    assert.equal(textarea.value, '{')
    fireEvent.change(textarea, { target: { value: '{"gpt-6-sol":"kimi-k3"}' } })
    fireEvent.click(view.getByRole('button', { name: 'Visual Mode' }))
    assert.deepEqual(
      inputs(view).map((input) => input.value),
      ['gpt-6-sol', 'kimi-k3']
    )
  })

  test('still loads real external replacements and clears', () => {
    const editor = (value: string) => (
      <I18nextProvider i18n={i18n}>
        <ModelMappingEditor value={value} onChange={() => {}} />
      </I18nextProvider>
    )
    const view = render(editor(initialValue))
    view.rerender(editor('{"gpt-6-sol":"kimi-k3"}'))
    assert.deepEqual(
      inputs(view).map((input) => input.value),
      ['gpt-6-sol', 'kimi-k3']
    )
    view.rerender(editor(''))
    assert.equal(view.queryAllByRole('textbox').length, 0)
  })

  test('keeps template filling and deleting the final row working', () => {
    const view = renderControlled('')
    fireEvent.click(view.getByRole('button', { name: 'Fill Template' }))
    assert.deepEqual(
      inputs(view).map((input) => input.value),
      ['gpt-3.5-turbo', 'gpt-3.5-turbo-0125']
    )
    fireEvent.click(deleteButton(inputs(view)[0]))
    assert.equal(view.queryAllByRole('textbox').length, 0)
    assert.equal(view.emittedValues.at(-1), '')
  })
})
