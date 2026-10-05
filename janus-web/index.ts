// const worker = new Worker("./worker.ts");

// worker.postMessage("hello");
// worker.onmessage = event => {
//   console.log(event.data);
// };

// Boolean
let isActive: boolean = true;
let hasPermission = false;

// Number
let decimal: number = 6;
let hex: number = 0xf00d;
let binary: number = 0b1010;
let octal: number = 0o744;
let float: number = 3.14;

// String
let color: string = 'blue';
let fullName: string = 'John Doe';
let age: number = 30;
let sentance: string = `Hello, my nameis ${fullName} and I'll be ${age + 1} next year.`;

const hugeNumber: bigint = BigInt(9007199254740991);

let scores: number[] = [100, 95, 98];

function greet(name: string): string {
  return `Hello ${name}!`;
}

const user = {
  name: 'Alice',
  age: 30,
  isAdmin: true,
};

console.log(user)

scores.forEach((element) => {
  console.log(element);
  console.log(greet(element.toString()));
});
