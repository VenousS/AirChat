const { spawn } = require('child_process');
const path = require('path');

document.addEventListener('DOMContentLoaded', () => {
    // Элементы интерфейса
    const elements = {
        loginContainer: document.getElementById('login-container'),
        chatContainer: document.getElementById('chat-container'),
        connectButton: document.getElementById('connectButton'),
        createRoomBtn: document.getElementById('createRoomBtn'),
        messageInput: document.getElementById('message-input'),
        sendBtn: document.getElementById('sendBtn'),
        voiceChatBtn: document.getElementById('voiceChatBtn'),
        exitChatBtn: document.getElementById('exitChatBtn'),
        messageArea: document.getElementById('message-area'),
        usernameInput: document.getElementById('username'),
        ipInput: document.getElementById('server-ip')
    };

    let currentUser = '';
    let isVoiceChatActive = false;
    let clientProcess = null;
    let serverProcess = null;

    // Инициализация приложения
    const init = () => {
        elements.loginContainer.style.display = 'block';
        elements.chatContainer.style.display = 'none';
        elements.messageArea.innerHTML = '';
        elements.usernameInput.value = '';
        elements.ipInput.value = '127.0.0.1'; // Установка дефолтного IP
        
        // Завершаем процессы при выходе
        if (clientProcess) {
            clientProcess.kill();
            clientProcess = null;
        }
        if (serverProcess) {
            serverProcess.kill();
            serverProcess = null;
        }
    };

    init();

    // Получаем путь к ресурсам
    const getAppPath = () => {
        return process.env.NODE_ENV === 'development' 
            ? path.join(__dirname, '..') 
            : path.join(process.resourcesPath, 'app');
    };

    // Создать комнату (запустить server.exe)
    elements.createRoomBtn.addEventListener('click', () => {
        const appPath = getAppPath();
        const serverPath = path.join(appPath, 'bin', 'server.exe');
        
        // Если сервер уже запущен
        if (serverProcess) {
            addSystemMessage('Сервер уже запущен');
            return;
        }

        serverProcess = spawn(serverPath, [], {
            cwd: path.join(appPath, 'bin'),
            shell: true
        });

        serverProcess.stdout.on('data', (data) => {
            console.log('Server:', data.toString());
            addSystemMessage(`Сервер: ${data.toString().trim()}`);
        });

        serverProcess.stderr.on('data', (data) => {
            console.error('Server Error:', data.toString());
            addSystemMessage(`Ошибка сервера: ${data.toString().trim()}`);
        });

        serverProcess.on('exit', (code) => {
            console.log(`Server exited with code ${code}`);
            addSystemMessage(`Сервер завершил работу (код ${code})`);
            serverProcess = null;
        });

        addSystemMessage('Сервер чата запущен');
    });

    // Войти в чат (запустить client.exe)
    elements.connectButton.addEventListener('click', () => {
        const username = elements.usernameInput.value.trim();
        const serverIP = elements.ipInput.value.trim() || '127.0.0.1';

        if (!username) {
            alert('Введите имя пользователя');
            return;
        }

        currentUser = username;
        elements.loginContainer.style.display = 'none';
        elements.chatContainer.style.display = 'block';
        addSystemMessage(`Вы вошли как ${username}, подключение к ${serverIP}`);

        const appPath = getAppPath();
        const clientPath = path.join(appPath, 'bin', 'client.exe');
        
        clientProcess = spawn(clientPath, [serverIP, username], {
            cwd: path.join(appPath, 'bin'),
            shell: true
        });

        clientProcess.stdout.on('data', (data) => {
            const message = data.toString().trim();
            if (message.includes('joined the chat')) {
                addSystemMessage(message);
            } else {
                addMessage(message.split(':')[0], message.split(':')[1].trim(), 'other');
            }
        });

        clientProcess.stderr.on('data', (data) => {
            addSystemMessage(`Ошибка: ${data.toString().trim()}`);
        });

        clientProcess.on('close', (code) => {
            addSystemMessage(`Соединение закрыто (код ${code})`);
            clientProcess = null;
        });
    });

    // Отправка сообщений
    const sendMessage = () => {
        const message = elements.messageInput.value.trim();
        if (message && clientProcess) {
            clientProcess.stdin.write(message + '\n');
            addMessage(currentUser, message, 'user');
            elements.messageInput.value = '';
        }
    };

    elements.sendBtn.addEventListener('click', sendMessage);
    elements.messageInput.addEventListener('keypress', (e) => {
        if (e.key === 'Enter') sendMessage();
    });

    // Голосовой чат (заглушка)
    elements.voiceChatBtn.addEventListener('click', () => {
        isVoiceChatActive = !isVoiceChatActive;
        if (isVoiceChatActive) {
            elements.voiceChatBtn.innerHTML = '🔴 Завершить голосовой чат';
            elements.voiceChatBtn.style.backgroundColor = '#ff5252';
            addSystemMessage('Голосовой чат активирован (реализация в разработке)');
        } else {
            elements.voiceChatBtn.innerHTML = '🎤 Голосовой чат';
            elements.voiceChatBtn.style.backgroundColor = '';
            addSystemMessage('Голосовой чат деактивирован');
        }
    });

    // Выход из чата
    elements.exitChatBtn.addEventListener('click', init);

    // Добавление сообщений в чат
    function addMessage(user, text, type) {
        const messageElement = document.createElement('div');
        messageElement.className = `message ${type}`;
        messageElement.innerHTML = `
            <div class="message-info">${user} • ${new Date().toLocaleTimeString()}</div>
            <div class="message-text">${text}</div>
        `;
        elements.messageArea.appendChild(messageElement);
        elements.messageArea.scrollTop = elements.messageArea.scrollHeight;
    }

    function addSystemMessage(text) {
        addMessage('Система', text, 'system');
    }
});