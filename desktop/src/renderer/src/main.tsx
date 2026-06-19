import ReactDOM from 'react-dom/client';
import { App } from './App';
import { installGlobalDiagnostics } from './diag';
import './styles.css';

installGlobalDiagnostics();

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(<App />);
