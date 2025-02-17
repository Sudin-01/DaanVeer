const fetchPromise = fetch('http://localhost:8080/transaction/pool');
const selectTxOptions = document.getElementById("txSelect");

selectTxOptions.addEventListener("submit", e => {
	e.preventDefault();
});

fetchPromise
	.then(response => {
		if (!response.ok) {
			throw new Error(`HTTP error: ${response.status}`);
		}
		return response.json();
	})
	.then(json => {
		let latestMinedTransactions = json;
		console.log(latestMinedTransactions)

		let tblBody2 = document.getElementById("minedTransactions");
		latestMinedTransactions.forEach((bData, index) => {
			let row = document.createElement("tr");
			let subRow = document.createElement("tr");
			row.setAttribute("id", bData.id);
			row.setAttribute("class", "row");
			subRow.setAttribute("class", `${bData.id}_expand expandContent`)
			row.innerHTML = `
				<td>${index}</td>
                <td><a href="#" class="txId">${bData.txID}</a></td>
                <td class="amountTransacted"><b class="">${bData.value}</b><br></td>
                <td><div class="dateString">${bData.timestamp}</div></td>
            `;
			subRow.innerHTML = `
                <td colspan="4">
                    <div class="expandFlex">
                        <div class="fromHolder">
                            <b>From</b>
                            <a href="#">${bData.senderHash}</a>
                        </div>
                        <div class="toHolder">
                            <b>To</b>
                            <a href="#">${bData.recipientHash}</a>
                        </div>
                    </div>
                </td>
            `;
			tblBody2.append(row);
			tblBody2.append(subRow);

			let selectOption = document.createElement("input");
			let selectLabel = document.createElement("label");
			let indexString = index.toString();

			selectOption.type = "checkbox";
			selectOption.value = indexString;
			selectOption.name = indexString;

			selectLabel.for = indexString;
			selectLabel.innerText = indexString;

			selectTxOptions.append(selectOption);
			selectTxOptions.append(selectLabel);
		});

		selectTxOptions.addEventListener("submit", e => {
			e.preventDefault();

			let selectedTxsIndex = document.querySelectorAll('input[type="checkbox"]:checked');
			let selectedTxs = []
			selectedTxsIndex.forEach(TxIndex => {
				selectedTxs.push(latestMinedTransactions[TxIndex.value])
			})
			console.log("sending the transaction: ", selectedTxs)
			postData("http://localhost:8080/block/mine", selectedTxs);

		});
	});

async function postData(url = '', data = {}) {
    // Default options are marked with *

    const response = await fetch(url, {
        method: 'POST', // *GET, POST, PUT, DELETE, etc.
        mode: 'cors', // no-cors, *cors, same-origin
        cache: 'no-cache', // *default, no-cache, reload, force-cache, only-if-cached
        credentials: 'same-origin', // include, *same-origin, omit
        headers: {
            'Content-Type': 'application/json'
            // 'Content-Type': 'application/x-www-form-urlencoded',
        },
        redirect: 'follow', // manual, *follow, error
        referrerPolicy: 'no-referrer', // no-referrer, *no-referrer-when-downgrade, origin, origin-when-cross-origin, same-origin, strict-origin, strict-origin-when-cross-origin, unsafe-url
        body: JSON.stringify(data) // body data type must match "Content-Type" header
    });
	console.log(data)
    return response.json(); // parses JSON response into native JavaScript objects
}