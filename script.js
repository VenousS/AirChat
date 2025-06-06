document.addEventListener('DOMContentLoaded', () => {
    const themeButtons = document.querySelectorAll('.theme-switcher button');
    const htmlElement = document.documentElement;

    // Function to apply a theme
    const applyTheme = (themeName) => {
        htmlElement.setAttribute('data-theme', themeName);
        localStorage.setItem('selectedTheme', themeName);
    };

    // Load saved theme from localStorage
    const savedTheme = localStorage.getItem('selectedTheme');
    if (savedTheme) {
        applyTheme(savedTheme);
    } else {
        // Default to light theme if no theme is saved
        applyTheme('light'); 
    }

    // Add event listeners to theme buttons
    themeButtons.forEach(button => {
        button.addEventListener('click', () => {
            const themeName = button.getAttribute('data-theme');
            applyTheme(themeName);
        });
    });

    const attachFileButton = document.getElementById('attachFileBtn');
    const fileInputElement = document.getElementById('fileInput');
    const messageInputField = document.getElementById('message-input-field');
    const sendButton = document.getElementById('sendBtn');
    const uiFileNameDisplay = document.getElementById('uiFileNameDisplay');

    let selectedFilePath = null;
    let selectedFileObject = null;

    if (attachFileButton && fileInputElement && messageInputField && sendButton) {
        attachFileButton.addEventListener('click', function() {
            fileInputElement.click();
        });

        fileInputElement.addEventListener('change', function(event) {
            const files = event.target.files;
            if (files.length > 0) {
                const file = files[0];
                selectedFileObject = file;

                if (file.path) {
                    selectedFilePath = file.path;
                    console.log('Выбран файл (UI):', file.name, 'Путь:', selectedFilePath);
                    if (uiFileNameDisplay) {
                        uiFileNameDisplay.textContent = `Прикреплен: ${file.name}`;
                    } else {
                        messageInputField.placeholder = `Файл: ${file.name}`;
                    }
                    sendButton.textContent = 'Отправить файл';
                } else {
                    console.warn('Не удалось получить полный путь к файлу. Отправка через Go клиент командой /sendfile может быть невозможна без доработок в Electron main процессе.');
                    if (uiFileNameDisplay) {
                        uiFileNameDisplay.textContent = `Прикреплен: ${file.name} (путь недоступен)`;
                    } else {
                        messageInputField.placeholder = `Файл: ${file.name} (путь недоступен)`;
                    }
                    selectedFilePath = null;
                }
            }
        });

        sendButton.addEventListener('click', function() {
            const messageText = messageInputField.value.trim();

            if (selectedFilePath && selectedFileObject) {
                const commandForGoClient = `/sendfile ${selectedFilePath}`;
                console.warn(`КОНЦЕПТУАЛЬНО: Сейчас нужно передать эту команду в stdin вашего запущенного Go клиента:`);
                console.warn(commandForGoClient);
                
                alert(`Инициирована отправка файла: ${selectedFileObject.name}.\nКоманда для Go клиента (см. консоль разработчика):\n${commandForGoClient}\n\nУбедитесь, что ваш Go клиент запущен и может принять эту команду.`);

                selectedFilePath = null;
                selectedFileObject = null;
                fileInputElement.value = '';
                if (uiFileNameDisplay) {
                    uiFileNameDisplay.textContent = '';
                }
                messageInputField.placeholder = 'Введите сообщение...';
                sendButton.textContent = 'Отправить';

                if (messageText !== '') {
                    console.log(`Текстовое сообщение "${messageText}" нужно отправить отдельно.`);
                }
                messageInputField.value = '';

            } else if (messageText !== '') {
                console.log(`Отправка текстового сообщения: ${messageText}`);
                alert(`ОТПРАВКА ТЕКСТА: ${messageText}\n(Логика отправки обычного сообщения должна быть здесь)`);
                messageInputField.value = '';

            } else {
                console.log('Нет сообщения или файла для отправки.');
            }
        });

    } else {
        if (!attachFileButton) console.error('Кнопка #attachFileBtn не найдена');
        if (!fileInputElement) console.error('Элемент #fileInput не найден');
        if (!messageInputField) console.error('Поле #message-input-field не найдено');
        if (!sendButton) console.error('Кнопка #sendBtn не найдена');
    }
}); 