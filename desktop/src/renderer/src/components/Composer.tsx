import { KeyboardEvent, useState } from 'react';

type ComposerProps = {
  onSend: (value: string) => void;
  disabled?: boolean;
};

export function Composer({ onSend, disabled = false }: ComposerProps): JSX.Element {
  const [value, setValue] = useState('');

  const send = (): void => {
    if (disabled) {
      return;
    }
    const text = value.trimEnd();
    if (!text) {
      return;
    }
    onSend(`${text}\r`);
    setValue('');
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>): void => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      send();
    }
  };

  return (
    <div className={`composer${disabled ? ' composer--disabled' : ''}`}>
      <textarea
        value={value}
        disabled={disabled}
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={onKeyDown}
        placeholder={
          disabled
            ? 'Select a workspace to message its agent…'
            : 'Message the agent…  Enter sends, Shift+Enter adds a newline'
        }
        rows={2}
      />
      <button type="button" onClick={send} disabled={disabled}>
        Send
      </button>
    </div>
  );
}
