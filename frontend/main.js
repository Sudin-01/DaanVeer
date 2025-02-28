const fetchPromise = fetch('http://localhost:8080/my-wallet/address');
const fetchBalance = fetch('http://localhost:8080/my-wallet/balance')

fetchPromise
    .then(response => {
        if (!response.ok) {
            throw new Error(`HTTP error: ${response.status}`);
        }
        return response.json();
    })
    .then(json => {
            console.log(json)
            document.getElementById("address").innerHTML = json.address
            document.getElementById("pkhash").innerHTML = json.public_key_hash
            document.getElementById("pk").innerHTML = json.public_key
        });

fetchBalance
    .then(response => {
        if (!response.ok) {
            throw new Error(`HTTP error: ${response.status}`);
        }
        return response.json();
    })
    .then(json => {
            document.getElementById("balance").innerHTML = json.balance
            
        });