const clock = document.querySelector("#clock");
const year = document.querySelector("#year");

function updateClock() {
  clock.textContent = new Intl.DateTimeFormat(undefined, {
    hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
  }).format(new Date());
}

updateClock();
year.textContent = new Date().getFullYear();
window.setInterval(updateClock, 1000);
