const { ipcRenderer } = require('electron');

document.addEventListener('DOMContentLoaded', () => {
    const elements = {
        statusIndicator: document.getElementById('status-indicator'),
        serverIp: document.getElementById('server-ip'),
        connectedUsers: document.getElementById('connected-users'),
        voiceUsers: document.getElementById('voice-users'),
        startServerBtn: document.getElementById('startServerBtn'),
        stopServerBtn: document.getElementById('stopServerBtn'),
        joinChatBtn: document.getElementById('joinChatBtn'),
        logContainer: document.getElementById('log-container')
    };

    let serverRunning = false;

    // Функция для добавления лога
    function addLog(message, type = 'info') {
        const logEntry = document.createElement('div');
        logEntry.className = `log-entry ${type}`;
        logEntry.textContent = `[${new Date().toLocaleTimeString()}] ${message}`;
        elements.logContainer.appendChild(logEntry);
        elements.logContainer.scrollTop = elements.logContainer.scrollHeight;
    }

    // Обновление статуса сервера
    function updateServerStatus(isRunning) {
        serverRunning = isRunning;
        elements.statusIndicator.textContent = isRunning ? 'Online' : 'Offline';
        elements.statusIndicator.className = `status-indicator ${isRunning ? 'online' : 'offline'}`;
        elements.startServerBtn.disabled = isRunning;
        elements.stopServerBtn.disabled = !isRunning;
    }

    // Запуск сервера
    elements.startServerBtn.addEventListener('click', () => {
        ipcRenderer.send('start-server');
        updateServerStatus(true);
        addLog('Starting server...');
    });

    // Остановка сервера
    elements.stopServerBtn.addEventListener('click', () => {
        ipcRenderer.send('stop-server');
        updateServerStatus(false);
        addLog('Stopping server...');
    });

    // Присоединиться к чату
    elements.joinChatBtn.addEventListener('click', () => {
        ipcRenderer.send('join-chat');
    });

    // Обработка событий от main процесса
    ipcRenderer.on('server-log', (event, message) => {
        addLog(message);
    });

    ipcRenderer.on('update-users', (event, { total, voice }) => {
        elements.connectedUsers.textContent = total;
        elements.voiceUsers.textContent = voice;
    });

    ipcRenderer.on('server-error', (event, message) => {
        addLog(message, 'error');
        updateServerStatus(false);
    });

    // Получение IP адреса
    fetch('https://api.ipify.org?format=json')
        .then(response => response.json())
        .then(data => {
            elements.serverIp.textContent = data.ip;
        })
        .catch(() => {
            elements.serverIp.textContent = '127.0.0.1';
        });
}); 