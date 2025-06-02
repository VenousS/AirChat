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
}); 