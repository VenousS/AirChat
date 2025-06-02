const { spawn } = require('child_process');
const path = require('path');
const { ipcRenderer } = require('electron');
// const { app } = require('electron'); // Убрали, так как app недоступен здесь

document.addEventListener('DOMContentLoaded', () => {
    // Элементы интерфейса
    const elements = {
        loginContainer: document.getElementById('login-container'),
        serverContainer: document.getElementById('server-container'),
        chatContainer: document.getElementById('chat-container'),
        connectButton: document.getElementById('connectButton'),
        createRoomBtn: document.getElementById('createRoomBtn'),
        messageInput: document.getElementById('message-input'),
        sendBtn: document.getElementById('sendBtn'),
        voiceChatBtn: document.getElementById('voiceChatBtn'),
        exitChatBtn: document.getElementById('exitChatBtn'),
        messageArea: document.getElementById('message-area'),
        usernameInput: document.getElementById('username'),
        ipInput: document.getElementById('server-ip'),
        passwordInput: document.getElementById('password'), // Новое поле
        // Элементы управления сервером
        statusIndicator: document.getElementById('status-indicator'),
        serverIpDisplay: document.getElementById('server-ip-display'),
        connectedUsers: document.getElementById('connected-users'),
        voiceUsers: document.getElementById('voice-users'),
        startServerBtn: document.getElementById('startServerBtn'),
        stopServerBtn: document.getElementById('stopServerBtn'),
        backToLoginBtn: document.getElementById('backToLoginBtn'),
        logContainer: document.getElementById('log-container'),
        userListArea: document.getElementById('user-list-area')
    };

    let currentUser = '';
    let isVoiceChatActive = false;
    let clientProcess = null;
    let serverProcess = null;
    let serverRunning = false;
    let resourcesPathFromMain = null; // Будет установлено из main
    let isPackagedFromMain = null;    // Будет установлено из main
    let initialDataReceived = false;
    let userStatuses = {}; // Для хранения статусов { username: 'status' }
    let isAuthenticatedByClient = false; // Флаг успешной аутентификации

    // Получаем начальные данные от основного процесса
    ipcRenderer.on('initial-data', (event, data) => {
        addLog(`Received initial data from main: resourcesPath=${data.resourcesPath}, isPackaged=${data.isPackaged}`);
        resourcesPathFromMain = data.resourcesPath;
        isPackagedFromMain = data.isPackaged;
        initialDataReceived = true;
        // Можно здесь вызвать функции, которые должны были стартовать и зависят от этих данных,
        // но лучше проверять initialDataReceived перед их вызовом.
    });

    // Функция для обновления списка пользователей в UI
    function updateUserListUI() {
        if (!elements.userListArea) return;
        elements.userListArea.innerHTML = ''; // Очищаем список

        const sortedUsernames = Object.keys(userStatuses).sort((a, b) => {
            // Сначала текущий пользователь, если он есть
            if (a === currentUser && b !== currentUser) return -1;
            if (b === currentUser && a !== currentUser) return 1;
            // Затем по алфавиту
            return a.localeCompare(b);
        });

        for (const username of sortedUsernames) {
            const status = userStatuses[username];
            const userElement = document.createElement('div');
            userElement.className = 'user-list-item';

            const nameSpan = document.createElement('span');
            nameSpan.className = 'username';
            nameSpan.textContent = username === currentUser ? `${username} (Вы)` : username;

            const statusSpan = document.createElement('span');
            statusSpan.className = 'status';
            let statusText = '';
            let statusClass = '';

            switch (status) {
                case 'online':
                    statusText = 'В сети';
                    statusClass = 'status-online';
                    break;
                case 'in-voice':
                    statusText = 'В голосе';
                    statusClass = 'status-in-voice';
                    break;
                case 'offline':
                    statusText = 'Не в сети';
                    statusClass = 'status-offline';
                    // Для оффлайн пользователей можно сделать имя менее заметным
                    if (username !== currentUser) nameSpan.style.opacity = '0.6'; 
                    break;
                default:
                    statusText = 'Неизвестно';
                    statusClass = 'status-offline';
            }
            statusSpan.textContent = statusText;
            statusSpan.classList.add(statusClass);

            userElement.appendChild(nameSpan);
            userElement.appendChild(statusSpan);
            elements.userListArea.appendChild(userElement);
        }
    }

    // Инициализация приложения
    const init = () => {
        elements.loginContainer.style.display = 'block';
        elements.serverContainer.style.display = 'none';
        elements.chatContainer.style.display = 'none';
        elements.messageArea.innerHTML = '';
        elements.usernameInput.value = '';
        elements.ipInput.value = '127.0.0.1'; // Установка дефолтного IP
        elements.passwordInput.value = ''; // Очищаем поле пароля
        currentUser = '';
        isAuthenticatedByClient = false; // Сбрасываем флаг
        userStatuses = {};
        updateUserListUI();
        
        if (clientProcess) {
            clientProcess.kill();
            clientProcess = null;
        }
        stopServer(); 
    };

    init();

    const getAppPath = () => {
        const appPath = path.resolve('.');
        addLog(`Legacy app path resolved to: ${appPath}`);
        return appPath;
    };

    // Функции управления сервером
    function addLog(message, type = 'info') {
        const logEntry = document.createElement('div');
        logEntry.className = `log-entry ${type}`;
        logEntry.textContent = `[${new Date().toLocaleTimeString()}] ${message}`;
        elements.logContainer.appendChild(logEntry);
        elements.logContainer.scrollTop = elements.logContainer.scrollHeight;
    }

    function updateServerStatus(isRunning) {
        serverRunning = isRunning;
        elements.statusIndicator.textContent = isRunning ? 'Online' : 'Offline';
        elements.statusIndicator.className = `status-indicator ${isRunning ? 'online' : 'offline'}`;
        elements.startServerBtn.disabled = isRunning;
        elements.stopServerBtn.disabled = !isRunning;
    }

    // Функция для принудительного освобождения портов
    function killProcessOnPort(port) {
        if (process.platform === 'win32') {
            try {
                const findCommand = `netstat -ano | findstr :${port}`;
                const findProcess = spawn('cmd', ['/c', findCommand], { shell: true });
                
                findProcess.stdout.on('data', (data) => {
                    const lines = data.toString().split('\n');
                    lines.forEach(line => {
                        const parts = line.trim().split(/\s+/);
                        if (parts.length > 4) {
                            const pid = parts[4];
                            spawn('taskkill', ['/F', '/PID', pid], { shell: true });
                        }
                    });
                });
            } catch (error) {
                addLog(`Error killing process on port ${port}: ${error.message}`, 'error');
            }
        }
    }

    // Функция для остановки сервера
    function stopServer() {
        if (serverProcess && serverProcess.pid) {
            const pid = serverProcess.pid;
            addLog(`Attempting to stop server process tree (PID: ${pid})...`);
            try {
                if (process.platform === 'win32') {
                    const killProcess = spawn('taskkill', ['/F', '/T', '/PID', pid.toString()], { shell: true });
                    killProcess.stdout.on('data', (data) => addLog(`taskkill stdout: ${data}`));
                    killProcess.stderr.on('data', (data) => addLog(`taskkill stderr: ${data}`, 'error'));
                    killProcess.on('exit', (code) => addLog(`taskkill process exited with code ${code}`));
                } else {
                    process.kill(-pid);
                }
                setTimeout(() => {
                    if (serverProcess) { 
                        addLog('Forcing UI update as server process might not have emitted exit event in time.');
                        serverProcess.removeAllListeners();
                        serverProcess = null;
                        updateServerStatus(false);
                        elements.connectedUsers.textContent = '0';
                        elements.voiceUsers.textContent = '0';
                    }
                }, 2000);
            } catch (error) {
                addLog(`Error attempting to kill server process tree (PID: ${pid}): ${error.message}`, 'error');
                serverProcess = null; 
                updateServerStatus(false);
                elements.connectedUsers.textContent = '0';
                elements.voiceUsers.textContent = '0';
            }
        } else if (serverProcess) {
            try {
                serverProcess.kill();
            } catch (error) {
                 addLog(`Error killing server process (no PID): ${error.message}`, 'error');
            }
            serverProcess = null;
            updateServerStatus(false);
            elements.connectedUsers.textContent = '0';
            elements.voiceUsers.textContent = '0';
        }
    }

    // Обертка для запуска серверной логики
    function attemptRunServerLogic(actionFunction, buttonSource) {
        if (!initialDataReceived) {
            addLog(`Waiting for initial data from main process before ${buttonSource ? 'handling ' + buttonSource + ' click' : 'starting server'}...`, 'warn');
            // Можно показать пользователю сообщение или просто подождать
            // Повторная попытка через короткое время
            setTimeout(() => attemptRunServerLogic(actionFunction, buttonSource), 500);
            return;
        }
        actionFunction(); // Выполняем переданную функцию, когда данные готовы
    }

    function executeStartServer(sourceButtonName) {
        if (serverProcess) {
            addLog(`Server process already running or attempt in progress (from ${sourceButtonName}).`, 'info');
            return;
        }

        addLog(`Preparing to start server (from ${sourceButtonName})...`);
        updateServerStatus(false); 
        elements.statusIndicator.textContent = 'Starting...';

        killProcessOnPort(6000);
        killProcessOnPort(6001);

        setTimeout(() => {
            try {
                const isDev = !isPackagedFromMain; // Используем значение из main
                const serverExecutableName = 'server.exe';

                let serverPath;
                let serverCwd;

                if (isDev) {
                    serverPath = path.join(path.resolve('.'), 'bin', serverExecutableName);
                    serverCwd = path.join(path.resolve('.'), 'bin');
                } else {
                    // Для упакованного приложения файлы теперь в resources/app/bin/
                    const appBinPath = path.join(resourcesPathFromMain, 'app', 'bin');
                    serverPath = path.join(appBinPath, serverExecutableName);
                    serverCwd = appBinPath;
                }
                
                addLog(`Resolved server path: ${serverPath}`);
                addLog(`Resolved server CWD: ${serverCwd}`);

                if (!require('fs').existsSync(serverPath)) {
                    addLog(`Error: Server executable not found at ${serverPath}. Ensure it's built ('npm run build:go') and correctly placed.`, 'error');
                    elements.statusIndicator.textContent = 'Error: Not Found';
                    elements.statusIndicator.className = 'status-indicator offline';
                    return;
                }

                addLog('Starting server process...');
                serverProcess = spawn(serverPath, [], {
                    cwd: serverCwd,
                    stdio: ['pipe', 'pipe', 'pipe'] 
                });

                if (!serverProcess || !serverProcess.pid) {
                    addLog('Error: Failed to create server process or process has no PID.', 'error');
                    elements.statusIndicator.textContent = 'Error: Failed to start';
                    elements.statusIndicator.className = 'status-indicator offline';
                    serverProcess = null; 
                    return;
                }
                addLog(`Server process created successfully with PID: ${serverProcess.pid}`);

                const handleServerStartMessage = (message) => {
                    if (message.includes('Сервер запущен на порту :6000')) {
                        updateServerStatus(true); 
                        const ipMatch = message.match(/\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\b/);
                        const serverIP = ipMatch ? ipMatch[0] : '127.0.0.1';
                        elements.serverIpDisplay.textContent = serverIP;
                        elements.ipInput.value = serverIP;
                        return true; // Сообщение обработано
                    }
                    return false; // Сообщение не о запуске сервера
                };

                serverProcess.stdout.on('data', (data) => {
                    const message = data.toString();
                    const isErrorMessage = message.toLowerCase().includes('error') || message.toLowerCase().includes('ошибка') || message.toLowerCase().includes('failed') || message.toLowerCase().includes('panic');
                    addLog(message, isErrorMessage ? 'error' : 'info');
                    
                    if (!handleServerStartMessage(message)) {
                        // Дополнительная обработка других сообщений из stdout, если необходимо
                        if (message.includes('Новый клиент:')) {
                            elements.connectedUsers.textContent = (parseInt(elements.connectedUsers.textContent) || 0) + 1;
                        } else if (message.includes('вошёл в голосовой чат')) {
                            elements.voiceUsers.textContent = (parseInt(elements.voiceUsers.textContent) || 0) + 1;
                        } else if (message.includes('отключился от голосового чата') || message.includes('вышел из голосового чата')) {
                            const currentVoiceUsers = parseInt(elements.voiceUsers.textContent) || 0;
                            if (currentVoiceUsers > 0) {
                                elements.voiceUsers.textContent = currentVoiceUsers - 1;
                            }
                        }
                    }
                });

                serverProcess.stderr.on('data', (data) => {
                    const message = data.toString();
                    // Пытаемся обработать как сообщение о старте сервера
                    const serverStarted = handleServerStartMessage(message);
                    
                    // Логируем всегда, но тип лога может зависеть от того, было ли это сообщение о старте
                    // или это реальная ошибка.
                    // Если это было сообщение о старте, оно уже не является ошибкой в контексте Electron UI.
                    // Можно добавить более сложную логику, если сервер может писать и ошибки, и инфо в stderr.
                    const isErrorMessageForLog = !serverStarted && (message.toLowerCase().includes('error') || 
                                                                message.toLowerCase().includes('ошибка') || 
                                                                message.toLowerCase().includes('failed') || 
                                                                message.toLowerCase().includes('panic'));
                    addLog(message, isErrorMessageForLog ? 'error' : 'info');

                    if (!serverStarted) {
                        // Обработка других сообщений из stderr, если это не ошибка и не сообщение о старте
                        // (например, другие информационные сообщения от Go log)
                        if (message.includes('Новый клиент:')) {
                            elements.connectedUsers.textContent = (parseInt(elements.connectedUsers.textContent) || 0) + 1;
                        } else if (message.includes('вошёл в голосовой чат')) {
                            elements.voiceUsers.textContent = (parseInt(elements.voiceUsers.textContent) || 0) + 1;
                        } else if (message.includes('отключился от голосового чата') || message.includes('вышел из голосового чата')) {
                            const currentVoiceUsers = parseInt(elements.voiceUsers.textContent) || 0;
                            if (currentVoiceUsers > 0) {
                                elements.voiceUsers.textContent = currentVoiceUsers - 1;
                            }
                        }
                    }
                });

                serverProcess.on('error', (err) => {
                    addLog(`Server process error: ${err.message}`, 'error');
                    updateServerStatus(false);
                    serverProcess = null; 
                });

                serverProcess.on('exit', (code, signal) => {
                    addLog(`Server process exited with code ${code} and signal ${signal}`, code !== 0 ? 'error' : 'info');
                    updateServerStatus(false);
                    elements.connectedUsers.textContent = '0';
                    elements.voiceUsers.textContent = '0';
                    serverProcess = null; 
                });

            } catch (error) {
                addLog(`Critical error starting server: ${error.message}\n${error.stack}`, 'error');
                updateServerStatus(false);
                elements.statusIndicator.textContent = 'Error';
                elements.statusIndicator.className = 'status-indicator offline';
                serverProcess = null;
            }
        }, 500); 
    }

    // Создать комнату (показать панель управления сервером)
    elements.createRoomBtn.addEventListener('click', () => {
        elements.loginContainer.style.display = 'none';
        elements.serverContainer.style.display = 'block';
        elements.serverIpDisplay.textContent = '127.0.0.1 (Ожидание ответа сервера)'; 
        elements.ipInput.value = '127.0.0.1';
        
        attemptRunServerLogic(() => executeStartServer('Create Room button'), 'Create Room button');
    });

    elements.startServerBtn.addEventListener('click', () => {
        attemptRunServerLogic(() => executeStartServer('Start Server button'), 'Start Server button');
    });

    elements.stopServerBtn.addEventListener('click', () => {
        addLog('Stop Server button clicked.');
        stopServer();
    });

    window.addEventListener('beforeunload', () => {
        if (serverProcess) {
            stopServer();
        }
        if (clientProcess) {
            clientProcess.kill(); // Убедимся, что и клиент убит
        }
    });

    // Войти в чат (запустить client.exe)
    elements.connectButton.addEventListener('click', () => {
        if (!initialDataReceived) { // Проверка для client.exe
            addLog('Waiting for initial data from main process before connecting client...', 'warn');
            setTimeout(() => elements.connectButton.click(), 500); // Простая повторная попытка
            return;
        }

        const username = elements.usernameInput.value.trim();   
        const serverIP = elements.ipInput.value.trim() || '127.0.0.1';
        const password = elements.passwordInput.value; // Не trim(), пароль может содержать пробелы

        if (!username) {
            alert('Введите имя пользователя');
            return;
        }
        if (!password) {
            alert('Введите пароль');
            return;
        }

        currentUser = username;
        elements.loginContainer.style.display = 'none';
        elements.chatContainer.style.display = 'block';
        addSystemMessage(`Попытка входа как ${username} на сервер ${serverIP}...`);

        const isDev = !isPackagedFromMain;
        const clientExecutableName = 'client.exe';
        
        let clientPath;
        let clientCwd;

        if (isDev) {
            clientPath = path.join(path.resolve('.'), 'bin', clientExecutableName);
            clientCwd = path.join(path.resolve('.'), 'bin');
        } else {
            // Для упакованного приложения файлы теперь в resources/app/bin/
            const appBinPath = path.join(resourcesPathFromMain, 'app', 'bin');
            clientPath = path.join(appBinPath, clientExecutableName);
            clientCwd = appBinPath;
        }

        addLog(`Resolved client path: ${clientPath}`);
        addLog(`Resolved client CWD: ${clientCwd}`);

        if (!require('fs').existsSync(clientPath)) {
            addSystemMessage(`Ошибка: client.exe не найден по пути ${clientPath}. Выполните 'npm run build:go'.`);
            // Вернуть пользователя или показать ошибку более явно
            elements.chatContainer.style.display = 'none';
            elements.loginContainer.style.display = 'block';
            return;
        }
        
        isAuthenticatedByClient = false; // Сбрасываем перед новой попыткой
        clientProcess = spawn(clientPath, [serverIP, username, password], { // Добавлен пароль
            cwd: clientCwd,
            stdio: ['pipe', 'pipe', 'pipe'] 
        });

        clientProcess.stdout.on('data', (data) => {
            const rawMessages = data.toString().trim().split('\n'); // Сообщения могут приходить пачками
            rawMessages.forEach(rawMessage => {
                const message = rawMessage.trim();
                addLog(`Client STDOUT: ${message}`); // Логируем вывод клиента

                if (message.startsWith('Успешный вход как ')) {
                    const loggedInUsername = message.substring('Успешный вход как '.length).replace('.', '');
                    currentUser = loggedInUsername;
                    isAuthenticatedByClient = true;
                    elements.loginContainer.style.display = 'none';
                    elements.serverContainer.style.display = 'none'; // Скрываем и серверную панель, если она была открыта
                    elements.chatContainer.style.display = 'block';
                    addSystemMessage(`Вы вошли как ${currentUser}`);
                    // Первоначальный статус для себя
                    userStatuses[currentUser] = 'online';
                    updateUserListUI(); 
                    return; // Больше ничего не делаем с этим сообщением
                }
                
                if (message.startsWith('Ошибка входа: ')) {
                    const reason = message.substring('Ошибка входа: '.length);
                    alert(`Ошибка входа: ${reason}`);
                    // Остаемся на экране логина, init() не вызываем, чтобы поля не сбрасывались
                    if(clientProcess) clientProcess.kill(); // Убиваем процесс клиента, так как вход не удался
                    clientProcess = null;
                    isAuthenticatedByClient = false;
                    return;
                }

                if (message.startsWith('Ваша сессия была завершена')) {
                    alert(message); 
                    init(); // Возврат на экран входа
                    return;
                }

                if (!isAuthenticatedByClient) {
                    // Игнорируем другие сообщения, если еще не было 'Успешный вход'
                    // Это может быть 'Доступные команды:' и т.д., которые клиент печатает до LOGIN_SUCCESS
                    if (!message.startsWith('Попытка подключения к серверу') && 
                        !message.startsWith('Использование: client.exe') &&
                        !message.startsWith('IP сервера не может быть пустым') &&
                        !message.startsWith('Имя пользователя не может быть пустым') &&
                        !message.startsWith('Пароль не может быть пустым') &&
                        !message.startsWith('Ошибка разрешения адреса:') &&
                        !message.startsWith('Ошибка подключения:') &&
                        !message.startsWith('Не удалось подключиться к серверу') &&
                        !message.startsWith('Ошибка инициализации PortAudio:')
                    ) {
                         addSystemMessage(message); // Выводим системные сообщения от клиента, если они не являются ошибками запуска
                    }
                    return;
                }

                // Обработка сообщений ПОСЛЕ успешной аутентификации
                if (message.startsWith('STATUS_UPDATE::')) {
                    const parts = message.split('::');
                    if (parts.length === 3) {
                        const updatedUser = parts[1];
                        const newStatus = parts[2];
                        userStatuses[updatedUser] = newStatus;
                        updateUserListUI();
                    }
                } else if (message.startsWith('USER_LIST::')) {
                    try {
                        const jsonPart = message.substring('USER_LIST::'.length);
                        const userList = JSON.parse(jsonPart);
                        const newStatuses = {}; // Собираем новые статусы
                        userList.forEach(user => {
                            newStatuses[user.username] = user.status;
                        });
                        // Убедимся, что текущий пользователь всегда есть и он online, если список его не содержит
                        // (хотя сервер должен его включать)
                        if (currentUser && !newStatuses[currentUser]){
                            newStatuses[currentUser] = userStatuses[currentUser] || 'online'; 
                        }
                        userStatuses = newStatuses; // Заменяем старые статусы новыми
                        updateUserListUI();
                    } catch (e) {
                        addLog(`Error parsing USER_LIST: ${e.message}`, 'error');
                    }
                } else if (message.includes(' joined the chat')) {
                    addSystemMessage(message);
                    const userJoined = message.split(' ')[0]; 
                    if (userJoined && userJoined !== currentUser && !userStatuses[userJoined]){
                        userStatuses[userJoined] = 'online';
                        updateUserListUI();
                    }
                } else if (message.includes('left the chat')) { 
                    addSystemMessage(message);
                    const userLeft = message.split(' ')[0];
                    if (userLeft && userStatuses[userLeft]){
                        userStatuses[userLeft] = 'offline'; 
                        updateUserListUI();
                    }
                } else if (message.includes(':') && !message.startsWith('STATUS_UPDATE::') && !message.startsWith('USER_LIST::')) { // Обычное сообщение чата, исключаем спец команды
                    const parts = message.split(':');
                    const user = parts[0];
                    const text = parts.slice(1).join(':').trim();
                    addMessage(user, text, 'other');
                } else if (message === `Вы подключились к голосовому чату`) { 
                    addSystemMessage(message);
                    isVoiceChatActive = true; // <<< Обновляем состояние
                    elements.voiceChatBtn.innerHTML = '🔴 Завершить VC'; 
                    elements.voiceChatBtn.classList.add('voice-active');
                    if(currentUser) userStatuses[currentUser] = 'in-voice';
                    updateUserListUI();
                } else if (message === `Вы отключились от голосового чата`) { 
                    addSystemMessage(message);
                    isVoiceChatActive = false; // <<< Обновляем состояние
                    elements.voiceChatBtn.innerHTML = '🎤 Голосовой чат';
                    elements.voiceChatBtn.classList.remove('voice-active');
                    if(currentUser) userStatuses[currentUser] = 'online';
                    updateUserListUI();
                } else if (message.startsWith('ERROR::') || message.startsWith('SERVER_SHUTDOWN::')) {
                     addSystemMessage(message); // Выводим ошибки от сервера или сообщение о выключении
                } else {
                    // Системные сообщения от клиента, не требующие парсинга ника, или неизвестные
                    // addSystemMessage(`${message}`); // Можно раскомментировать для отладки неизвестных сообщений
                    // Логируем, чтобы не терять
                    if (message && !message.startsWith('Подключение к серверу') && !message.startsWith('Голосовое соединение установлено') && !message.startsWith('Используется устройство') && !message.startsWith('✅ Аудиопотоки инициализированы') && !message.startsWith('Доступные команды') && !message.startsWith('Использование: client.exe')) {
                        addSystemMessage(`${message}`);
                    }
                }
            });
        });

        clientProcess.stderr.on('data', (data) => {
            const errorMessage = data.toString().trim();
            addSystemMessage(`Клиент ошибка: ${errorMessage}`);
            addLog(`Client STDERR: ${errorMessage}`, 'error');
            // Если это ошибка, связанная с невозможностью запуска/работы клиента,
            // возможно, стоит вернуть на экран логина.
            // Но не все из stderr - это критическая ошибка для UI.
        });

        clientProcess.on('error', (err) => {
            addSystemMessage(`Ошибка запуска клиента: ${err.message}`);
            addLog(`Client process error: ${err.message}`, 'error');
            clientProcess = null;
            isAuthenticatedByClient = false;
            // Не вызываем init() здесь, чтобы пользователь мог видеть ошибку и попробовать снова
            // init(); 
        });

        clientProcess.on('close', (code) => {
            // Сообщение об успешном входе уже должно было переключить isAuthenticatedByClient
            // Если мы здесь и isAuthenticatedByClient все еще false, значит логин не удался
            // или клиент закрылся до успешного логина по другой причине.
            if (!isAuthenticatedByClient) {
                // Если код не 0 и это не штатный выход после LOGIN_FAILURE (там уже был alert)
                // и не SESSION_INVALIDATED (там тоже был alert и init)
                // то это может быть неожиданное закрытие клиента.
                if (code !== 0 && !message.startsWith('Ошибка входа:') && !message.startsWith('Ваша сессия была завершена')) {
                   // alert(`Соединение с клиентом неожиданно закрыто (код ${code}). Попробуйте снова.`);
                   addSystemMessage(`Соединение с клиентом закрыто (код ${code}). Если вход не удался, проверьте консоль клиента.`);
                }
                // init() здесь вызывать не нужно, если это был LOGIN_FAILURE, т.к. пользователь остался на экране логина.
                // Если это была ошибка запуска - тоже.
            } else {
                // Если был успешный вход, а потом клиент закрылся (например, по /exit)
                addSystemMessage(`Соединение с клиентом закрыто (код ${code})`);
            }
            addLog(`Client process exited with code ${code}`);
            clientProcess = null;
            isVoiceChatActive = false;
            elements.voiceChatBtn.innerHTML = '🎤 Голосовой чат';
            elements.voiceChatBtn.classList.remove('voice-active'); 
            // userStatuses = {}; // Не очищаем статусы, если это был /exit, и мы просто возвращаемся в init
            // updateUserListUI(); 
            if(isAuthenticatedByClient || code === 0) { // Если был успешный вход или штатный выход
                init(); // Возвращаемся на экран логина
            }
        });
    });

    // Отправка сообщений
    const sendMessage = () => {
        const messageText = elements.messageInput.value.trim();
        if (messageText && clientProcess && clientProcess.stdin && !clientProcess.stdin.destroyed) {
            try {
                clientProcess.stdin.write(messageText + '\n');
                addMessage(currentUser, messageText, 'user');
                elements.messageInput.value = '';
            } catch (error) {
                addSystemMessage(`Ошибка отправки сообщения: ${error.message}`);
                addLog(`Error writing to client stdin: ${error.message}`, 'error');
            }
        } else if (!clientProcess || !clientProcess.stdin || clientProcess.stdin.destroyed) {
            addSystemMessage('Клиент не подключен. Невозможно отправить сообщение.');
        }
    };

    elements.sendBtn.addEventListener('click', sendMessage);
    elements.messageInput.addEventListener('keypress', (e) => {
        if (e.key === 'Enter') sendMessage();
    });

    // Голосовой чат
    elements.voiceChatBtn.addEventListener('click', () => {
        if (!clientProcess || !clientProcess.stdin || clientProcess.stdin.destroyed || !isAuthenticatedByClient) {
            addSystemMessage('Клиент не подключен или не аутентифицирован. Невозможно управлять голосовым чатом.');
            return;
        }
        // Не меняем isVoiceChatActive и UI здесь, ждем подтверждения от stdout клиента
        try {
            if (!isVoiceChatActive) { // Если хотим войти
                clientProcess.stdin.write('/voice\n');
                addSystemMessage('Попытка подключения к голосовому чату...');
                // Статус обновится по получению сообщения "Вы подключились к голосовому чату"
            } else { // Если хотим выйти
                clientProcess.stdin.write('/leave\n');
                addSystemMessage('Попытка отключения от голосового чата...');
                // Статус обновится по получению сообщения "Вы отключились от голосового чата"
            }
        } catch (error) {
            addSystemMessage(`Ошибка управления голосовым чатом: ${error.message}`);
            addLog(`Error writing to client stdin (voice): ${error.message}`, 'error');
        }
    });

    // Выход из чата
    elements.exitChatBtn.addEventListener('click', () => {
        if (clientProcess && clientProcess.stdin && !clientProcess.stdin.destroyed && isAuthenticatedByClient) {
            addSystemMessage('Отправка команды /exit клиенту...');
            try {
                clientProcess.stdin.write('/exit\n');
                // Не вызываем init() сразу, ждем закрытия clientProcess, который вызовет init()
            } catch (error) {
                addSystemMessage(`Ошибка отправки /exit: ${error.message}`);
                addLog(`Error writing /exit to client stdin: ${error.message}`, 'error');
                if (clientProcess) clientProcess.kill();
                init();
            }
        } else {
            addSystemMessage('Клиент не запущен, не аутентифицирован или уже отключается. Возврат на экран входа.');
            init();
        }
    });
    
    // Вернуться к окну входа
    elements.backToLoginBtn.addEventListener('click', () => {
        elements.serverContainer.style.display = 'none';
        elements.loginContainer.style.display = 'block';
        if (serverProcess) { // Если мы уходим с админки сервера, а сервер работает, останавливаем его
            stopServer();
        }
    });

    // Добавление сообщений в чат
    function addMessage(user, text, type) {
        const messageElement = document.createElement('div');
        messageElement.className = `message ${type}`;
        // Безопасное отображение текста, чтобы избежать XSS, если текст приходит от сервера/клиента
        const textNode = document.createTextNode(text);
        const textDiv = document.createElement('div');
        textDiv.className = 'message-text';
        textDiv.appendChild(textNode);

        messageElement.innerHTML = `
            <div class="message-info">${document.createTextNode(user).textContent} • ${new Date().toLocaleTimeString()}</div>
        `;
        messageElement.appendChild(textDiv);
        elements.messageArea.appendChild(messageElement);
        elements.messageArea.scrollTop = elements.messageArea.scrollHeight;
    }

    function addSystemMessage(text) {
        addMessage('Система', text, 'system');
    }
});